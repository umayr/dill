package agentcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/umayr/dill/internal/preflight"
	"golang.org/x/sys/unix"
)

const journalStatusUsage = "usage: dill agent journal-status --state-dir /absolute/private/operations"

var (
	errJournalStatusDir     = errors.New("journal status requires an existing absolute owner-only state directory")
	errJournalStatusBusy    = errors.New("deployment journal is in use; offline status unavailable")
	errJournalStatusLock    = errors.New("cannot verify exclusive agent ownership is inactive")
	errJournalStatusMissing = errors.New("deployment journal is missing; no state was initialized")
	errJournalStatusFile    = errors.New("deployment journal must be a bounded owner-only regular file")
	errJournalStatusInvalid = errors.New("deployment journal encoding or records are invalid or unsupported")
	errJournalStatusOutput  = errors.New("cannot write journal status output")
)

type journalStatusSummary struct {
	Version                string `json:"version"`
	JournalVersion         int    `json:"journalVersion"`
	BytesUsed              int    `json:"bytesUsed"`
	ByteLimit              int    `json:"byteLimit"`
	LowCapacity            bool   `json:"lowCapacity"`
	AdmissionByteCeiling   int    `json:"admissionByteCeiling"`
	AdmissionHeadroomBytes int    `json:"admissionHeadroomBytes"`
	OutcomeReserveBytes    int    `json:"outcomeReserveBytes"`
	Records                int    `json:"records"`
	NotAdmitted            int    `json:"notAdmitted"`
	Succeeded              int    `json:"succeeded"`
	FailedNoEffects        int    `json:"failedNoEffects"`
	FailedPartial          int    `json:"failedPartial"`
	Unresolved             int    `json:"unresolved"`
}

func runJournalStatus(args []string, out, diagnostics io.Writer) error {
	fs := flag.NewFlagSet("dill agent journal-status", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Never echo arbitrary options or values.
	dir := fs.String("state-dir", "", "existing private deployment history directory")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		if _, err := fmt.Fprintln(diagnostics, journalStatusUsage); err != nil {
			return errJournalStatusOutput
		}
		return nil
	} else if err != nil || fs.NArg() != 0 || *dir == "" {
		return errors.New(journalStatusUsage)
	}
	summary, err := inspectJournalStatus(*dir)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(summary); err != nil {
		return errJournalStatusOutput
	}
	return nil
}

// Do not use openDeploymentHistory here: it can initialize, upgrade, compact,
// or sync journal state. Hold a shared lock on the existing directory inode so
// a cooperating agent cannot start writing while we inspect it.
func inspectJournalStatus(path string) (journalStatusSummary, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return journalStatusSummary{}, errJournalStatusDir
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return journalStatusSummary{}, errJournalStatusDir
	}
	defer unix.Close(fd)
	var dir unix.Stat_t
	if unix.Fstat(fd, &dir) != nil || dir.Mode&077 != 0 || int(dir.Uid) != os.Geteuid() {
		return journalStatusSummary{}, errJournalStatusDir
	}
	if err := unix.Flock(fd, unix.LOCK_SH|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return journalStatusSummary{}, errJournalStatusBusy
		}
		return journalStatusSummary{}, errJournalStatusLock
	}
	raw, err := readJournalStatusFile(fd)
	if err != nil {
		return journalStatusSummary{}, err
	}
	return summarizeJournalStatus(raw)
}

func readJournalStatusFile(dir int) ([]byte, error) {
	const name = "operations.json"
	valid := func(st unix.Stat_t) bool {
		return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&077 == 0 && int(st.Uid) == os.Geteuid() && st.Size <= historyByteLimit
	}
	// Refuse special files without opening them. Recheck the opened inode;
	// NOFOLLOW and NONBLOCK also protect against common path substitution.
	var before unix.Stat_t
	if err := unix.Fstatat(dir, name, &before, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return nil, errJournalStatusMissing
	} else if err != nil || !valid(before) {
		return nil, errJournalStatusFile
	}
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errJournalStatusFile
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !valid(st) || st.Dev != before.Dev || st.Ino != before.Ino {
		return nil, errJournalStatusFile
	}
	raw, err := io.ReadAll(io.LimitReader(f, historyByteLimit+1))
	if err != nil || len(raw) > historyByteLimit || int64(len(raw)) != st.Size {
		return nil, errJournalStatusFile
	}
	return raw, nil
}

// Use the live loader's pure record and ownership checks without calling its
// mutating load path. There is no engine enrollment here, only a digest-shape
// check rather than a comparison with the agent's configured target.
func summarizeJournalStatus(raw []byte) (journalStatusSummary, error) {
	var file deploymentHistoryFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&file) != nil || dec.Decode(new(any)) != io.EOF || file.Rows == nil || !preflight.ValidDigest(file.Target) ||
		(file.Version != 1 && file.Version != 2) || (file.Version == 1 && len(file.Rows) > legacyHistoryLimit) ||
		((file.Version == 2 || file.Journal != "") && !validApplyID(file.Journal)) {
		return journalStatusSummary{}, errJournalStatusInvalid
	}
	canonical, err := canonicalHistory(file)
	if err != nil || !bytes.Equal(canonical, raw) {
		return journalStatusSummary{}, errJournalStatusInvalid
	}

	ceiling := historyByteLimit - historyOutcomeSpace
	summary := journalStatusSummary{
		Version: "dill.agent-journal-status/1", JournalVersion: file.Version,
		BytesUsed: len(raw), ByteLimit: historyByteLimit, LowCapacity: len(raw) >= historyByteLimit*9/10,
		AdmissionByteCeiling: ceiling, AdmissionHeadroomBytes: max(0, ceiling-len(raw)), OutcomeReserveBytes: historyOutcomeSpace,
		Records: len(file.Rows),
	}
	seen := make(map[string]bool, len(file.Rows))
	latest := make(map[string]deploymentRecord)
	for _, r := range file.Rows {
		if seen[r.RequestID] || !(validDeploymentRecord(r) || file.Version == 2 && validCompactRecord(r)) ||
			(r.Mode != "fence" && !allowedDeploymentMode(recordNextMode(latest[r.Stack]), r.Mode)) {
			return journalStatusSummary{}, errJournalStatusInvalid
		}
		seen[r.RequestID] = true
		if !ignoresOwnership(r) {
			latest[r.Stack] = r
		}
		switch r.State {
		case "not-admitted":
			summary.NotAdmitted++
		case "succeeded":
			summary.Succeeded++
		case "failed":
			if r.Effects == "none" {
				summary.FailedNoEffects++
			} else {
				summary.FailedPartial++
			}
		case "pending", "unknown":
			summary.Unresolved++
		}
	}
	keep := ownershipPayloads(file.Rows)
	for _, r := range file.Rows {
		if keep[r.RequestID] && !validDeploymentRecord(r) {
			return journalStatusSummary{}, errJournalStatusInvalid
		}
	}
	return summary, nil
}
