package agentcmd

import (
	"bytes"
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

const historyLimit = 128

var errOperationConflict = errors.New("deployment operation conflicts with recorded intent")

type deploymentStatus struct {
	RequestID   string `json:"requestId"`
	Stack       string `json:"stack"`
	State       string `json:"state"`
	Effects     string `json:"effects"`
	OperationID string `json:"operationId,omitempty"`
}

type deploymentRecord struct {
	deploymentStatus
	Mode     string                      `json:"mode"`
	StageID  string                      `json:"stageId"`
	Manifest string                      `json:"manifest"`
	Owned    []operation.ManagedResource `json:"owned,omitempty"`
}

type deploymentHistoryFile struct {
	Version int                `json:"version"`
	Target  string             `json:"target"`
	Rows    []deploymentRecord `json:"operations"`
}

type deploymentHistory struct {
	mu       sync.Mutex
	dir      *os.File
	path     string
	target   string
	rows     []deploymentRecord
	active   map[string]bool
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
	const bound = 16 << 20
	path := filepath.Join(h.path, "operations.json")
	raw, err := readPrivateHTTPSFile(path, bound)
	if err != nil {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			// No mutating request is accepted before the first successful sync.
			return h.save(h.rows)
		}
		return err
	}
	var file deploymentHistoryFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&file) != nil || dec.Decode(new(any)) != io.EOF || file.Version != 1 || file.Target != h.target || file.Rows == nil || len(file.Rows) > historyLimit {
		return errors.New("invalid deployment history")
	}
	canonical, err := json.Marshal(file)
	if err != nil || !bytes.Equal(canonical, raw) {
		return errors.New("invalid deployment history encoding")
	}
	seen := make(map[string]bool, len(file.Rows))
	latest := make(map[string]deploymentRecord)
	for _, r := range file.Rows {
		if seen[r.RequestID] || !validDeploymentRecord(r) || !allowedDeploymentMode(recordNextMode(latest[r.Stack]), r.Mode) {
			return errors.New("invalid deployment history record")
		}
		seen[r.RequestID] = true
		if r.State != "failed" || r.Effects != "none" {
			latest[r.Stack] = r
		}
	}
	h.rows = file.Rows
	return h.dir.Sync()
}

func (h *deploymentHistory) save(rows []deploymentRecord) error {
	b, err := json.Marshal(deploymentHistoryFile{Version: 1, Target: h.target, Rows: rows})
	if err != nil {
		return err
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
	return h.dir.Sync()
}

// managedLocked ignores only failures that prove no engine effects. A
// removal is a tombstone: retained resources are never silently adopted.
func (h *deploymentHistory) managedLocked(stack string) (deploymentRecord, string) {
	if h.poisoned {
		return deploymentRecord{}, ""
	}
	for i := len(h.rows) - 1; i >= 0; i-- {
		r := h.rows[i]
		if r.Stack != stack || (r.State == "failed" && r.Effects == "none") {
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
	if len(h.rows) >= historyLimit || !allowedDeploymentMode(mode, row.Mode) || previous.RequestID != expectedPrevious || row.State != "pending" || !validDeploymentRecord(row) {
		return errOperationConflict
	}
	for _, r := range h.rows {
		if r.RequestID == row.RequestID {
			return errOperationConflict
		}
	}
	rows := append(append([]deploymentRecord{}, h.rows...), row)
	if err := h.save(rows); err != nil {
		h.poisoned = true
		return errOperationConflict
	}
	h.rows = rows
	h.active[row.RequestID] = true
	return nil
}

func validDeploymentRecord(r deploymentRecord) bool {
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
		if row.RequestID == result.RequestID && row.Stack == result.Stack && row.State == "pending" {
			rows := append([]deploymentRecord{}, h.rows...)
			rows[i].deploymentStatus, rows[i].Owned = result, owned
			if !validDeploymentRecord(rows[i]) {
				return errOperationConflict
			}
			if err := h.save(rows); err != nil {
				h.poisoned = true
				return errOperationConflict
			}
			h.rows = rows
			return nil
		}
	}
	return errOperationConflict
}

func (h *deploymentHistory) status(id string) (deploymentStatus, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
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
