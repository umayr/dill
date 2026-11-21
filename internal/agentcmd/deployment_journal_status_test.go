package agentcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"golang.org/x/sys/unix"
)

func journalStatusFixture(t *testing.T, version int, rows []deploymentRecord) []byte {
	t.Helper()
	var journal string
	if version >= 2 {
		journal = strings.Repeat("a", 32)
	}
	raw, err := canonicalHistory(deploymentHistoryFile{Version: version, Target: operation.Digest("test target"), Journal: journal, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJournalStatusFixture(t *testing.T, dir string, raw []byte) string {
	t.Helper()
	path := filepath.Join(dir, "operations.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func journalStatusFence(n int, stack string) deploymentRecord {
	return deploymentRecord{deploymentStatus: deploymentStatus{RequestID: fmt.Sprintf("%032x", n), Stack: stack, State: "not-admitted", Effects: "none"}, Mode: "fence"}
}

func journalStatusRow(n int, stack, mode, state, effects string) deploymentRecord {
	r := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: fmt.Sprintf("%032x", n), Stack: stack, State: state, Effects: effects},
		Mode: mode, StageID: strings.Repeat("b", 32), Manifest: strings.Repeat("c", 64)}
	if state == "succeeded" || effects == "partial" {
		r.OperationID = "op_" + r.RequestID
	}
	return r
}

func TestJournalStatusCLI(t *testing.T) {
	create := journalStatusRow(1, "private-stack", "create", "succeeded", "complete")
	create.StageID, create.Manifest = "", "" // Compacted before successful removal.
	remove := journalStatusRow(2, create.Stack, "remove", "succeeded", "complete")
	remove.StageID, remove.Manifest = "", "" // Retained tombstone.
	managed := journalStatusRow(8, "managed", "create", "succeeded", "complete")
	managed.Owned = []operation.ManagedResource{
		{Kind: "container", Name: "private-container", ID: strings.Repeat("d", 64)},
		{Kind: "network", Name: "private-network", ID: strings.Repeat("e", 64)},
	}
	rows := []deploymentRecord{
		create, remove, journalStatusFence(3, create.Stack),
		journalStatusRow(4, "pending", "create", "pending", "unknown"),
		journalStatusRow(5, "unknown", "create", "unknown", "unknown"),
		journalStatusRow(6, "failure", "create", "failed", "none"),
		journalStatusRow(7, "partial", "create", "failed", "partial"),
		managed,
	}
	for _, tc := range []struct {
		name     string
		version  int
		rows     []deploymentRecord
		combined bool
		want     journalStatusSummary
	}{
		{"v2 mixed records", 2, rows, true, journalStatusSummary{Records: 8, NotAdmitted: 1, Succeeded: 3, FailedNoEffects: 1, FailedPartial: 1, Unresolved: 2}},
		{"v1 with no journal identity", 1, []deploymentRecord{journalStatusFence(3, create.Stack)}, false, journalStatusSummary{Records: 1, NotAdmitted: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := historyDirectory(t)
			raw := journalStatusFixture(t, tc.version, tc.rows)
			path := writeJournalStatusFixture(t, dir, raw)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.want
			want.Version, want.JournalVersion = "dill.agent-journal-status/1", tc.version
			want.BytesUsed, want.ByteLimit, want.OutcomeReserveBytes = len(raw), historyByteLimit, historyOutcomeSpace
			want.AdmissionByteCeiling = historyByteLimit - historyOutcomeSpace
			want.AdmissionHeadroomBytes = want.AdmissionByteCeiling - len(raw)
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			if code := Main([]string{"journal-status", "--state-dir", dir}, &out, &diagnostics, tc.combined); code != 0 || diagnostics.Len() != 0 || out.String() != string(wantJSON)+"\n" {
				t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
			}
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatalf("journal metadata changed: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("journal bytes changed: %v", err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 1 || files[0].Name() != "operations.json" {
				t.Fatalf("directory contents changed: %v %v", files, err)
			}
		})
	}
}

func TestJournalStatusLowCapacity(t *testing.T) {
	dir := historyDirectory(t)
	fence := journalStatusFence(0, strings.Repeat("x", 128))
	base := len(journalStatusFixture(t, 2, []deploymentRecord{}))
	step := len(journalStatusFixture(t, 2, []deploymentRecord{fence})) - base + 1
	// Simulate a journal after committing an outcome has used some of the
	// reserved budget, without actually admitting or fencing an operation.
	count := (historyByteLimit - base + 1) / step
	rows := make([]deploymentRecord, count)
	for i := range rows {
		rows[i] = journalStatusFence(i, fence.Stack)
	}
	raw := journalStatusFixture(t, 2, rows)
	writeJournalStatusFixture(t, dir, raw)
	got, err := inspectJournalStatus(dir)
	if err != nil || !got.LowCapacity || got.AdmissionHeadroomBytes != 0 || got.BytesUsed != len(raw) || got.NotAdmitted != count {
		t.Fatalf("status=%+v error=%v", got, err)
	}
}

func TestJournalStatusRejectsCorruptHistory(t *testing.T) {
	fence := journalStatusFence(1, "sample")
	missingOwnership := journalStatusRow(2, "sample", "create", "succeeded", "complete")
	missingOwnership.StageID, missingOwnership.Manifest = "", ""
	valid := journalStatusFixture(t, 2, []deploymentRecord{fence})
	for name, raw := range map[string][]byte{
		"duplicate request IDs": journalStatusFixture(t, 2, []deploymentRecord{fence, fence}),
		"absent ownership":      journalStatusFixture(t, 2, []deploymentRecord{missingOwnership}),
		"invalid mode chain":    journalStatusFixture(t, 2, []deploymentRecord{journalStatusRow(3, "sample", "update", "pending", "unknown")}),
		"future version":        journalStatusFixture(t, 3, []deploymentRecord{}),
		"noncanonical JSON":     append(bytes.Clone(valid), '\n'),
		"trailing content":      append(bytes.Clone(valid), []byte(`{"secret":"not for output"}`)...),
		"truncated JSON":        valid[:len(valid)-1],
	} {
		t.Run(name, func(t *testing.T) {
			dir := historyDirectory(t)
			writeJournalStatusFixture(t, dir, raw)
			var out, diagnostics bytes.Buffer
			if code := Main([]string{"journal-status", "--state-dir", dir}, &out, &diagnostics, true); code != 1 || out.Len() != 0 || diagnostics.String() != "dill-agent: "+errJournalStatusInvalid.Error()+"\n" {
				t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
			}
		})
	}
}

func TestJournalStatusRefusesLiveOrMissingJournal(t *testing.T) {
	dir := historyDirectory(t)
	if _, err := inspectJournalStatus(dir); !errors.Is(err, errJournalStatusMissing) {
		t.Fatalf("missing journal: %v", err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("inspect created files: %v %v", files, err)
	}
	h, err := openDeploymentHistory(dir, operation.Digest("test target"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.dir.Close() })
	if _, err := inspectJournalStatus(dir); !errors.Is(err, errJournalStatusBusy) {
		t.Fatalf("locked journal: %v", err)
	}
	if err := h.dir.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := inspectJournalStatus(dir); err != nil || got.Records != 0 {
		t.Fatalf("unlocked journal: %+v %v", got, err)
	}
}

func TestJournalStatusRefusesUnsafePaths(t *testing.T) {
	for _, name := range []string{"directory symlink", "directory permissions", "file symlink", "file permissions", "file is directory", "file is FIFO", "oversize file"} {
		t.Run(name, func(t *testing.T) {
			dir := historyDirectory(t)
			path := filepath.Join(dir, "operations.json")
			raw := journalStatusFixture(t, 2, []deploymentRecord{})
			want := errJournalStatusFile
			var err error
			switch name {
			case "directory symlink":
				parent := historyDirectory(t)
				err = os.Symlink(dir, filepath.Join(parent, "alias"))
				dir, want = filepath.Join(parent, "alias"), errJournalStatusDir
			case "directory permissions":
				err, want = os.Chmod(dir, 0755), errJournalStatusDir
			case "file symlink":
				other := writeJournalStatusFixture(t, historyDirectory(t), raw)
				err = os.Symlink(other, path)
			case "file permissions":
				writeJournalStatusFixture(t, dir, raw)
				err = os.Chmod(path, 0644)
			case "file is directory":
				err = os.Mkdir(path, 0700)
			case "file is FIFO":
				err = unix.Mkfifo(path, 0600)
			case "oversize file":
				writeJournalStatusFixture(t, dir, raw)
				err = os.Truncate(path, historyByteLimit+1)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, got := inspectJournalStatus(dir); !errors.Is(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestJournalStatusRejectsUnsupportedArgumentsAndOutputFailure(t *testing.T) {
	dir := historyDirectory(t)
	writeJournalStatusFixture(t, dir, journalStatusFixture(t, 2, []deploymentRecord{}))
	for _, args := range [][]string{{}, {"--state-dir", "relative"}, {"--state-dir", dir, "unexpected"}, {"--state-dir", dir, "--force"}, {"--private-input"}} {
		var out, diagnostics bytes.Buffer
		if code := Main(append([]string{"journal-status"}, args...), &out, &diagnostics, true); code != 1 || out.Len() != 0 || strings.Contains(diagnostics.String(), "private-input") || strings.Contains(diagnostics.String(), dir) {
			t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
		}
	}
	var out, diagnostics bytes.Buffer
	if code := Main([]string{"journal-status", "--help"}, &out, &diagnostics, true); code != 0 || out.Len() != 0 || diagnostics.String() != journalStatusUsage+"\n" {
		t.Fatalf("help: code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
	}
	diagnostics.Reset()
	if code := Main([]string{"journal-status", "--state-dir", dir}, journalStatusBrokenWriter{}, &diagnostics, true); code != 1 || diagnostics.String() != "dill-agent: "+errJournalStatusOutput.Error()+"\n" {
		t.Fatalf("output failure: code=%d diagnostics=%q", code, diagnostics.String())
	}
}

type journalStatusBrokenWriter struct{}

func (journalStatusBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("private writer error")
}
