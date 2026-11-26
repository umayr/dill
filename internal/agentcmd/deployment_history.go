package agentcmd

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"golang.org/x/sys/unix"
)

const (
	legacyHistoryLimit = 128
	historyByteLimit   = 16 << 20
	// Each valid owned resource encodes fewer than MaxNameBytes + 128 bytes.
	// Reserve an outcome's worst-case expansion before admitting work. Fences
	// use the same limit so they cannot consume this space during execution.
	historyOutcomeSpace = preflight.MaxResources*(preflight.MaxNameBytes+128) + 1024
)

var (
	errOperationConflict = errors.New("deployment operation conflicts with recorded intent")
	errHistoryCapacity   = errors.New("deployment journal capacity reached; preserve the journal and inspect agent state")
)

type deploymentStatus struct {
	RequestID   string `json:"requestId"`
	Action      string `json:"action,omitempty"`
	Stack       string `json:"stack"`
	State       string `json:"state"`
	Effects     string `json:"effects"`
	OperationID string `json:"operationId,omitempty"`
}

type deploymentRecord struct {
	deploymentStatus
	Mode     string                      `json:"mode"`
	StageID  string                      `json:"stageId,omitempty"`
	Manifest string                      `json:"manifest,omitempty"`
	Owned    []operation.ManagedResource `json:"owned,omitempty"`
}

type deploymentHistoryFile struct {
	Version int                `json:"version"`
	Target  string             `json:"target"`
	Rows    []deploymentRecord `json:"operations"`
	Journal string             `json:"journalId,omitempty"`
}

type deploymentHistory struct {
	mu       sync.Mutex
	dir      *os.File
	path     string
	target   string
	journal  string // immutable after load; binds a request to one durable history
	rows     []deploymentRecord
	active   map[string]bool
	bytes    int
	poisoned bool
}

func openDeploymentHistory(path, target string) (*deploymentHistory, error) {
	if err := checkHTTPSDir(path); err != nil || !preflight.ValidDigest(target) {
		return nil, errors.New("deployment history requires a private directory")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("deployment history unavailable")
	}
	dir := os.NewFile(uintptr(fd), "deployment history")
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		dir.Close()
		return nil, errors.New("deployment history already open or unsupported")
	}
	h := &deploymentHistory{dir: dir, path: path, target: target, active: make(map[string]bool), rows: []deploymentRecord{}}
	if err := h.load(); err != nil {
		dir.Close()
		return nil, errors.New("deployment history unavailable; inspect private state")
	}
	return h, nil
}

func (h *deploymentHistory) load() error {
	path := filepath.Join(h.path, "operations.json")
	raw, err := readPrivateHTTPSFile(path, historyByteLimit)
	if err != nil {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			// No mutating request is accepted before the first successful sync.
			return h.initializeJournal()
		}
		return err
	}
	var file deploymentHistoryFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&file) != nil || dec.Decode(new(any)) != io.EOF || (file.Version != 1 && file.Version != 2) || file.Target != h.target || file.Rows == nil || (file.Version == 1 && len(file.Rows) > legacyHistoryLimit) {
		return errors.New("invalid deployment history")
	}
	canonical, err := canonicalHistory(file)
	if err != nil || !bytes.Equal(canonical, raw) || (file.Version == 2 && !validApplyID(file.Journal)) {
		return errors.New("invalid deployment history encoding")
	}
	seen := make(map[string]bool, len(file.Rows))
	latest := make(map[string]deploymentRecord)
	for _, r := range file.Rows {
		if seen[r.RequestID] || !(validDeploymentRecord(r) || file.Version == 2 && validCompactRecord(r)) || (r.Mode != "fence" && !allowedDeploymentMode(recordNextMode(latest[r.Stack]), r.Mode)) {
			return errors.New("invalid deployment history record")
		}
		seen[r.RequestID] = true
		if !ignoresOwnership(r) {
			latest[r.Stack] = r
		}
	}
	keep := ownershipPayloads(file.Rows)
	for _, r := range file.Rows {
		if keep[r.RequestID] && !validDeploymentRecord(r) {
			return errors.New("current operation lacks ownership evidence")
		}
	}
	h.rows, h.bytes = file.Rows, len(raw)
	if file.Journal == "" {
		// Upgrade existing history without discarding ownership or outcomes.
		return h.initializeJournal()
	}
	if !validApplyID(file.Journal) {
		return errOperationConflict
	}
	h.journal = file.Journal
	if file.Version == 1 {
		return h.save(compactHistory(h.rows), 0)
	}
	return h.dir.Sync()
}

// The original encoding included empty snapshot fields on fences.
func canonicalHistory(file deploymentHistoryFile) ([]byte, error) {
	if file.Version != 1 {
		return json.Marshal(file)
	}
	type legacyRecord struct {
		deploymentStatus
		Mode     string                      `json:"mode"`
		StageID  string                      `json:"stageId"`
		Manifest string                      `json:"manifest"`
		Owned    []operation.ManagedResource `json:"owned,omitempty"`
	}
	legacy := struct {
		Version int            `json:"version"`
		Target  string         `json:"target"`
		Rows    []legacyRecord `json:"operations"`
		Journal string         `json:"journalId,omitempty"`
	}{file.Version, file.Target, make([]legacyRecord, 0, len(file.Rows)), file.Journal}
	for _, r := range file.Rows {
		legacy.Rows = append(legacy.Rows, legacyRecord{r.deploymentStatus, r.Mode, r.StageID, r.Manifest, r.Owned})
	}
	return json.Marshal(legacy)
}

func (h *deploymentHistory) initializeJournal() error {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	h.journal = hex.EncodeToString(id[:])
	return h.save(compactHistory(h.rows), 0)
}

func (h *deploymentHistory) save(rows []deploymentRecord, headroom int) error {
	b, err := json.Marshal(deploymentHistoryFile{Version: 2, Target: h.target, Rows: rows, Journal: h.journal})
	if err != nil {
		return err
	}
	if len(b) > historyByteLimit-headroom {
		return errHistoryCapacity
	}
	f, err := os.CreateTemp(h.path, ".operations-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(b)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(h.path, "operations.json")); err != nil {
		return err
	}
	if err := h.dir.Sync(); err != nil {
		return err
	}
	h.rows, h.bytes = rows, len(b)
	return nil
}

// Drop snapshot and resource payloads only when they can no longer influence
// admission. Request IDs, terminal results and the mode chain are permanent.
func compactHistory(rows []deploymentRecord) []deploymentRecord {
	keep := ownershipPayloads(rows)
	for i := range rows {
		r := &rows[i]
		if !keep[r.RequestID] {
			r.StageID, r.Manifest, r.Owned = "", "", nil
		}
	}
	return rows
}

// Keep unresolved intent and the last established ownership even when a
// subsequent operation on that stack left its effects unresolved.
func ownershipPayloads(rows []deploymentRecord) map[string]bool {
	keep := make(map[string]bool)
	settled := make(map[string]bool)
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if ignoresOwnership(r) {
			continue
		}
		if r.State != "succeeded" {
			keep[r.RequestID] = true
			continue
		}
		if !settled[r.Stack] && r.Mode != "remove" {
			keep[r.RequestID] = true
		}
		settled[r.Stack] = true
	}
	return keep
}

func validCompactRecord(r deploymentRecord) bool {
	return (r.Action == "" || validLifecycleAction(r.Action) && r.Mode == "update") && validApplyID(r.RequestID) && validDeployStack(r.Stack) && validDeploymentStatus(r.deploymentStatus) &&
		(r.Mode == "create" || r.Mode == "update" || r.Mode == "remove") &&
		r.StageID == "" && r.Manifest == "" && len(r.Owned) == 0 &&
		(r.State == "succeeded" || r.State == "failed" && r.Effects == "none")
}

func (h *deploymentHistory) nearCapacity() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bytes >= historyByteLimit*9/10
}

func ignoresOwnership(r deploymentRecord) bool {
	return r.Mode == "fence" || (r.State == "failed" && r.Effects == "none")
}

// managedLocked ignores fences and failures that prove no engine effects. A
// removal is a tombstone: retained resources are never silently adopted.
func (h *deploymentHistory) managedLocked(stack string) (deploymentRecord, string) {
	if h.poisoned {
		return deploymentRecord{}, ""
	}
	for i := len(h.rows) - 1; i >= 0; i-- {
		r := h.rows[i]
		if r.Stack != stack || ignoresOwnership(r) {
			continue
		}
		return r, recordNextMode(r)
	}
	return deploymentRecord{}, "create"
}

func recordNextMode(r deploymentRecord) string {
	if r.RequestID == "" {
		return "create"
	}
	if r.State == "succeeded" && r.Mode != "remove" {
		return "update"
	}
	return ""
}

func allowedDeploymentMode(next, actual string) bool {
	return next != "" && (actual == next || (next == "update" && actual == "remove"))
}

func (h *deploymentHistory) managed(stack string) (deploymentRecord, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.managedLocked(stack)
}

func (h *deploymentHistory) reserve(row deploymentRecord, expectedPrevious string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous, mode := h.managedLocked(row.Stack)
	if len(h.active) != 0 || !allowedDeploymentMode(mode, row.Mode) || previous.RequestID != expectedPrevious || row.State != "pending" || !validDeploymentRecord(row) {
		return errOperationConflict
	}
	for _, r := range h.rows {
		if r.RequestID == row.RequestID {
			return errOperationConflict
		}
	}
	rows := append(append([]deploymentRecord{}, h.rows...), row)
	if err := h.save(rows, historyOutcomeSpace); err != nil {
		h.poisoned = !errors.Is(err, errHistoryCapacity)
		return err
	}
	h.active[row.RequestID] = true
	return nil
}

// fence and reserve compete under the same lock and use the same durable
// journal. Even an apply that has already started preflight cannot pass a fence.
func (h *deploymentHistory) fence(id, stack string) (deploymentStatus, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.poisoned || !validApplyID(id) || !validDeployStack(stack) {
		return deploymentStatus{}, errOperationConflict
	}
	if status, exists := h.statusLocked(id); exists {
		if status.Stack != stack {
			return deploymentStatus{}, errOperationConflict
		}
		return status, nil
	}
	status := deploymentStatus{RequestID: id, Stack: stack, State: "not-admitted", Effects: "none"}
	rows := append(append([]deploymentRecord{}, h.rows...), deploymentRecord{deploymentStatus: status, Mode: "fence"})
	if err := h.save(rows, historyOutcomeSpace); err != nil {
		h.poisoned = !errors.Is(err, errHistoryCapacity)
		return deploymentStatus{}, err
	}
	return status, nil
}

func validDeploymentRecord(r deploymentRecord) bool {
	if r.Action != "" && (!validLifecycleAction(r.Action) || r.Mode != "update") {
		return false
	}
	if r.Mode == "fence" {
		return validApplyID(r.RequestID) && validDeployStack(r.Stack) && r.State == "not-admitted" && r.Effects == "none" && r.OperationID == "" && r.StageID == "" && r.Manifest == "" && len(r.Owned) == 0
	}
	if !validApplyID(r.RequestID) || !validDeployStack(r.Stack) || !validApplyID(r.StageID) || !preflight.ValidDigest("sha256:"+r.Manifest) || !validDeploymentStatus(r.deploymentStatus) ||
		(r.Mode != "create" && r.Mode != "update" && r.Mode != "remove") {
		return false
	}
	if r.State == "succeeded" && r.Mode != "remove" {
		return (operation.ManagedExpectation{Mode: "update", Resources: r.Owned}).Valid()
	}
	return len(r.Owned) == 0
}

func (h *deploymentHistory) finish(result deploymentStatus, owned []operation.ManagedResource) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.active, result.RequestID)
	if h.poisoned || !validDeploymentStatus(result) || result.State == "pending" {
		return errOperationConflict
	}
	for i, row := range h.rows {
		if row.RequestID == result.RequestID && row.Stack == result.Stack && row.Action == result.Action && row.State == "pending" {
			rows := append([]deploymentRecord{}, h.rows...)
			rows[i].deploymentStatus, rows[i].Owned = result, owned
			if !validDeploymentRecord(rows[i]) {
				return errOperationConflict
			}
			if err := h.save(compactHistory(rows), 0); err != nil {
				h.poisoned = true
				return errOperationConflict
			}
			return nil
		}
	}
	return errOperationConflict
}

func (h *deploymentHistory) status(id string) (deploymentStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusLocked(id)
}

func (h *deploymentHistory) statusLocked(id string) (deploymentStatus, bool) {
	for _, row := range h.rows {
		if row.RequestID == id {
			if row.State == "pending" {
				row.State = "unknown"
				if h.active[id] {
					row.State = "running"
				}
			}
			return row.deploymentStatus, true
		}
	}
	return deploymentStatus{}, false
}
