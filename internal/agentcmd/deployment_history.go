package agentcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/umayr/dill/internal/preflight"
	"golang.org/x/sys/unix"
)

const historyLimit = 1024

var errOperationConflict = errors.New("deployment operation conflicts with recorded intent")

type deploymentStatus struct {
	RequestID   string `json:"requestId"`
	Stack       string `json:"stack"`
	State       string `json:"state"`
	Effects     string `json:"effects"`
	OperationID string `json:"operationId,omitempty"`
}

type deploymentHistoryFile struct {
	Version int                `json:"version"`
	Target  string             `json:"target"`
	Rows    []deploymentStatus `json:"operations"`
}

type deploymentHistory struct {
	mu       sync.Mutex
	dir      *os.File
	path     string
	target   string
	rows     []deploymentStatus
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
	h := &deploymentHistory{dir: dir, path: path, target: target, active: make(map[string]bool), rows: []deploymentStatus{}}
	if err := h.load(); err != nil {
		dir.Close()
		return nil, errors.New("deployment history unavailable; inspect private state")
	}
	return h, nil
}

func (h *deploymentHistory) load() error {
	const bound = 512 << 10
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
	for _, r := range file.Rows {
		if !validApplyID(r.RequestID) || !validDeployStack(r.Stack) || seen[r.RequestID] || !validDeploymentStatus(r) {
			return errors.New("invalid deployment history record")
		}
		seen[r.RequestID] = true
	}
	h.rows = file.Rows
	return h.dir.Sync()
}

func (h *deploymentHistory) save(rows []deploymentStatus) error {
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

func (h *deploymentHistory) canReserve(id, stack string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.canReserveLocked(id, stack)
}

func (h *deploymentHistory) canReserveLocked(id, stack string) bool {
	if h.poisoned || len(h.rows) >= historyLimit {
		return false
	}
	for _, r := range h.rows {
		// V1 creates new stacks only. Do not adopt existing stacks, allow a
		// second creation, or replay a request with an ambiguous outcome.
		if r.RequestID == id || (r.Stack == stack && (r.State != "failed" || r.Effects != "none")) {
			return false
		}
	}
	return true
}

func (h *deploymentHistory) reserve(id, stack string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.canReserveLocked(id, stack) {
		return errOperationConflict
	}
	rows := append(append([]deploymentStatus{}, h.rows...), deploymentStatus{RequestID: id, Stack: stack, State: "pending", Effects: "unknown"})
	if err := h.save(rows); err != nil {
		h.poisoned = true
		return errOperationConflict
	}
	h.rows = rows
	h.active[id] = true
	return nil
}

func (h *deploymentHistory) finish(result deploymentStatus) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.active, result.RequestID)
	if h.poisoned || !validDeploymentStatus(result) || result.State == "pending" {
		return errOperationConflict
	}
	for i, row := range h.rows {
		if row.RequestID == result.RequestID && row.Stack == result.Stack && row.State == "pending" {
			rows := append([]deploymentStatus{}, h.rows...)
			rows[i] = result
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
			return row, true
		}
	}
	return deploymentStatus{}, false
}
