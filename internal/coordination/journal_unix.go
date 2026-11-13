//go:build linux || darwin

package coordination

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/umayr/dill/internal/stacklock"
	"golang.org/x/sys/unix"
)

const JournalVersion = "dill.coordination/1"
const maxRecord = 128 * 1024
const MaxResources = 256

// ResourceInput is the COMPLETE pre-mutation inventory, not engine output.
// Shared image deletion and unenumerated resources must be rejected by callers.
// Names never reach the journal. Locks and reservations are target-wide.
type ResourceInput struct{ Kind, Name, Action string }
type Resource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Action string `json:"action"`
}

// Record contains only closed vocabulary, random IDs and keyed fingerprints.
// Fingerprints are correlatable within private state, not public anonymization.
// Never publish the key or treat hashing secrets as declassification.
type Record struct {
	Version     string     `json:"version"`
	OperationID string     `json:"operationId"`
	Source      string     `json:"source"`
	Target      string     `json:"target"`
	Stack       string     `json:"stack"`
	Resources   []Resource `json:"resources"`
	State       string     `json:"state"`
	Outcome     string     `json:"outcome"`
	Effects     string     `json:"effects"`
}

// InspectionRequired deliberately contains no engine/config diagnostics. Record
// is nil for corrupt state, which this API cannot acknowledge. Forensic repair
// requires stopping ALL callers; deleting a lock or guessing effects is unsafe.
type InspectionRequired struct{ Record *Record }

func (e *InspectionRequired) Error() string {
	return "coordination state requires inspection; no mutation permitted"
}

// StartError correlates an operation whose Begin failed after its ID was
// allocated and state may have been installed. No mutation permission is given.
// Error text is fixed; only context cancellation/deadline may be unwrapped.
type StartError struct {
	OperationID string
	cause       error
}

func (e *StartError) Error() string { return "operation start not durable; inspection required" }
func (e *StartError) Unwrap() error { return e.cause }

// Session is not safe for concurrent use. Close releases locks without changing
// any record; incomplete operations remain unknown, never replay instructions.
type Session struct {
	state              *State
	locks              []*stacklock.Lock
	record             Record
	poisoned, finished bool
}

func (s *Session) OperationID() string { return s.record.OperationID }

func (s *State) scope(target, stack string) ([32]byte, error) {
	if !hexID(target, 64) || len(stack) == 0 || len(stack) > 1024 || strings.ContainsRune(stack, 0) {
		return [32]byte{}, errors.New("invalid coordination scope")
	}
	return s.stackKey(target, s.token("stack", stack)), nil
}
func (s *State) stackKey(target, stackToken string) [32]byte {
	return s.lockKey("stack-lock:"+target, stackToken)
}
func (s *State) resourceKey(target string, r Resource) [32]byte {
	return s.lockKey("resource-lock:"+target+":"+r.Kind, r.ID)
}
func hexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && hex.EncodeToString(b) == s
}
func nameFor(key [32]byte) string { return hex.EncodeToString(key[:]) + ".json" }
func resourceOK(r ResourceInput) bool {
	if r.Name == "" || len(r.Name) > 1024 || strings.ContainsRune(r.Name, 0) {
		return false
	}
	switch r.Kind {
	case "container", "network", "volume", "image":
	default:
		return false
	}
	switch r.Action {
	case "create", "remove", "start", "stop", "reconcile", "pull":
	default:
		return false
	}
	// Pulls serialize target-wide by image reference. Image deletion requires a
	// separate ownership/use policy and is deliberately unsupported.
	if r.Kind == "image" {
		return r.Action == "pull"
	}
	return r.Action != "pull"
}

// ValidateInventory checks the full static resource budget before target/state
// access. Begin repeats it defensively; callers must never qualify then discover
// that their declared inventory was unsupported all along.
func ValidateInventory(resources []ResourceInput) error {
	if len(resources) > MaxResources {
		return errors.New("unsupported resource inventory")
	}
	for _, resource := range resources {
		if !resourceOK(resource) {
			return errors.New("unsupported resource inventory")
		}
	}
	return nil
}

func keysFor(s *State, r Record) [][32]byte {
	keys := [][32]byte{s.stackKey(r.Target, r.Stack)}
	for _, resource := range r.Resources {
		keys = append(keys, s.resourceKey(r.Target, resource))
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i][:], keys[j][:]) < 0 })
	return keys
}
func (s *State) acquire(ctx context.Context, keys [][32]byte) ([]*stacklock.Lock, error) {
	var locks []*stacklock.Lock
	for i, key := range keys {
		if i > 0 && key == keys[i-1] {
			continue
		}
		lock, err := stacklock.AcquireAt(ctx, s.dir, key)
		if err != nil {
			release(locks)
			return nil, err
		}
		locks = append(locks, lock)
	}
	return locks, nil
}
func release(locks []*stacklock.Lock) error {
	var err error
	for i := len(locks) - 1; i >= 0; i-- {
		err = errors.Join(err, locks[i].Release())
	}
	return err
}

// Begin locks the whole inventory in sorted order, checks ALL stack/resource
// reservations (including other stacks), then durably writes the stack head
// followed by every resource head. No effects are permitted until it returns.
// Interrupted reservation installation is conservative: it may leave unknown
// state even though no mutation was started. No automatic replay/lock stealing.
func (s *State) Begin(ctx context.Context, target *Target, stack, source string, resources []ResourceInput) (*Session, error) {
	if target == nil {
		return nil, errors.New("qualified target required")
	}
	if _, err := s.scope(target.Identity(), stack); err != nil {
		return nil, err
	}
	if source != "cli" && source != "brine" || ValidateInventory(resources) != nil {
		return nil, errors.New("unsupported operation metadata")
	}
	record := Record{Version: JournalVersion, Source: source, Target: target.Identity(), Stack: s.token("stack", stack), Resources: []Resource{}, State: "in_progress", Outcome: "unknown", Effects: "unknown"}
	for _, r := range resources {
		if !resourceOK(r) {
			return nil, errors.New("unsupported resource inventory")
		}
		record.Resources = append(record.Resources, Resource{r.Kind, s.token("resource:"+r.Kind, r.Name), r.Action})
	}
	keys := keysFor(s, record)
	locks, err := s.acquire(ctx, keys)
	if err != nil {
		return nil, err
	}
	session := &Session{state: s, locks: locks, record: record}
	for _, key := range keys {
		old, err := s.load(nameFor(key), record.Target, "")
		if err != nil {
			session.Close()
			return nil, err
		}
		if old != nil && needsInspection(*old) {
			session.Close()
			return nil, &InspectionRequired{old}
		}
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		session.Close()
		return nil, errors.New("operation entropy unavailable")
	}
	session.record.OperationID = "op_" + hex.EncodeToString(id[:])
	// Stack first, so a partial reservation install cannot free the same stack.
	stackKey := s.stackKey(record.Target, record.Stack)
	if err = s.save(nameFor(stackKey), session.record); err == nil {
		for i, key := range keys {
			if key == stackKey || i > 0 && key == keys[i-1] {
				continue
			}
			if err = s.save(nameFor(key), session.record); err != nil {
				break
			}
		}
	}
	if err != nil {
		session.Close()
		return nil, &StartError{OperationID: session.record.OperationID}
	}
	if err = ctx.Err(); err != nil {
		session.Close()
		return nil, &StartError{OperationID: session.record.OperationID, cause: err}
	}
	return session, nil
}
func needsInspection(r Record) bool {
	return r.State != "acknowledged" && (r.State == "in_progress" || r.Outcome == "unknown" || r.Effects == "partial" || r.Effects == "unknown")
}
func axesOK(outcome, effects string) bool {
	switch outcome {
	case "succeeded", "changes", "invalid_input", "failed", "cancelled", "unknown":
	default:
		return false
	}
	switch effects {
	case "none", "complete", "partial", "unknown":
	default:
		return false
	}
	if outcome == "unknown" {
		return effects == "unknown"
	}
	if outcome == "invalid_input" || outcome == "changes" {
		return effects == "none"
	}
	if outcome == "succeeded" {
		return effects == "none" || effects == "complete"
	}
	return true
}
func resultName(id string, ack bool) string {
	if ack {
		return id + "-ack.result"
	}
	return id + ".result"
}
func commitName(id string, ack bool) string {
	if ack {
		return id + "-ack.commit"
	}
	return id + ".commit"
}

// publish NEVER replaces any in-progress head. The complete result is first
// independently durable. Only then does one atomic link syscall publish its
// authority. Nothing fallible follows that syscall. A lost link after crash
// causes conservative inspection; result bytes still have a durable filename.
// This relies on qualified LOCAL link semantics: a failed link creates no link.
// A filesystem with ambiguous syscall outcomes is unsupported, not repaired.
func (s *State) publish(r Record, ack bool) error {
	name := resultName(r.OperationID, ack)
	if err := s.save(name, r); err != nil {
		return err
	}
	if err := s.checkpoint("publishing"); err != nil {
		return err
	}
	if err := unix.Linkat(int(s.dir.Fd()), name, int(s.dir.Fd()), commitName(r.OperationID, ack), 0); err != nil {
		return errors.New("outcome publication failed")
	}
	return nil
}
func (s *Session) Finish(outcome, effects string) error {
	if s.finished || s.poisoned || len(s.locks) == 0 {
		return errors.New("operation cannot be finished")
	}
	if !axesOK(outcome, effects) {
		return errors.New("invalid terminal axes")
	}
	r := s.record
	r.State = "terminal"
	r.Outcome = outcome
	r.Effects = effects
	if err := s.state.publish(r, false); err != nil {
		s.poisoned = true
		return errors.New("terminal outcome not durable; effects unknown; inspection required")
	}
	s.finished = true
	return nil
}
func (s *Session) Close() error { err := release(s.locks); s.locks = nil; return err }
func (s *State) save(name string, r Record) error {
	data, err := json.Marshal(r)
	if err != nil || len(data) > maxRecord {
		return errors.New("invalid operation record")
	}
	return s.replace(name, append(data, '\n'))
}
func decodeRecord(data []byte) (*Record, error) {
	var r Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return nil, &InspectionRequired{}
	}
	canonical, _ := json.Marshal(r)
	if !bytes.Equal(data, append(canonical, '\n')) || !validRecord(r) {
		return nil, &InspectionRequired{}
	}
	return &r, nil
}

// readPublished accepts exactly the deliberate two-link result/commit pair,
// checks BOTH opened inodes, ownership, permissions and bounded regular-file
// shape. Other state files/keys/locks continue to require exactly one link.
func (s *State) readPublished(id string, ack bool) (*Record, error) {
	open := func(name string) (*os.File, *unix.Stat_t, error) {
		fd, err := unix.Openat(int(s.dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, nil, err
		}
		f := os.NewFile(uintptr(fd), name)
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 2 || st.Size > maxRecord {
			f.Close()
			return nil, nil, &InspectionRequired{}
		}
		return f, &st, nil
	}
	marker, mst, err := open(commitName(id, ack))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &InspectionRequired{}
	}
	defer marker.Close()
	result, rst, err := open(resultName(id, ack))
	if err != nil {
		return nil, &InspectionRequired{}
	}
	defer result.Close()
	if mst.Dev != rst.Dev || mst.Ino != rst.Ino {
		return nil, &InspectionRequired{}
	}
	data, err := io.ReadAll(io.LimitReader(result, maxRecord+1))
	if err != nil || len(data) > maxRecord {
		return nil, &InspectionRequired{}
	}
	return decodeRecord(data)
}
func sameOperation(head, result Record) bool {
	result.State = "in_progress"
	result.Outcome = "unknown"
	result.Effects = "unknown"
	a, _ := json.Marshal(head)
	b, _ := json.Marshal(result)
	return bytes.Equal(a, b)
}
func (s *State) load(name, target, stack string) (*Record, error) {
	data, err := s.readFile(name, maxRecord)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &InspectionRequired{}
	}
	head, err := decodeRecord(data)
	if err != nil || head.Target != target || (stack != "" && head.Stack != stack) || head.State != "in_progress" {
		return nil, &InspectionRequired{}
	}
	belongs := false
	for _, key := range keysFor(s, *head) {
		if nameFor(key) == name {
			belongs = true
			break
		}
	}
	if !belongs {
		return nil, &InspectionRequired{}
	}
	// Acknowledgement has separate immutable storage; partial/unknown evidence
	// from the original terminal result is retained, never overwritten.
	for _, ack := range []bool{true, false} {
		result, err := s.readPublished(head.OperationID, ack)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue
		}
		if !sameOperation(*head, *result) || (ack && result.State != "acknowledged") || (!ack && result.State != "terminal") {
			return nil, &InspectionRequired{}
		}
		return result, nil
	}
	return head, nil
}
func validRecord(r Record) bool {
	if r.Version != JournalVersion || !strings.HasPrefix(r.OperationID, "op_") || !hexID(strings.TrimPrefix(r.OperationID, "op_"), 32) || !hexID(r.Target, 64) || !hexID(r.Stack, 64) || r.Resources == nil || len(r.Resources) > MaxResources || r.Source != "cli" && r.Source != "brine" {
		return false
	}
	for _, v := range r.Resources {
		if !hexID(v.ID, 64) || !resourceOK(ResourceInput{v.Kind, "id", v.Action}) {
			return false
		}
	}
	switch r.State {
	case "in_progress", "acknowledged":
		return r.Outcome == "unknown" && r.Effects == "unknown"
	case "terminal":
		return axesOK(r.Outcome, r.Effects)
	}
	return false
}
func (s *State) inspect(ctx context.Context, key [32]byte, target, stack string) (*Record, error) {
	lock, err := stacklock.AcquireAt(ctx, s.dir, key)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	return s.load(nameFor(key), target, stack)
}

// Inspect and InspectResource are read-only, serialized, and usable offline.
func (s *State) Inspect(ctx context.Context, target, stack string) (*Record, error) {
	key, err := s.scope(target, stack)
	if err != nil {
		return nil, err
	}
	return s.inspect(ctx, key, target, s.token("stack", stack))
}
func validResourceScope(target, kind, name string) bool {
	action := "reconcile"
	if kind == "image" {
		action = "pull"
	}
	return hexID(target, 64) && resourceOK(ResourceInput{kind, name, action})
}
func (s *State) InspectResource(ctx context.Context, target, kind, name string) (*Record, error) {
	if !validResourceScope(target, kind, name) {
		return nil, errors.New("invalid resource scope")
	}
	return s.inspect(ctx, s.resourceKey(target, Resource{Kind: kind, ID: s.token("resource:"+kind, name)}), target, "")
}

// Acknowledge requires explicit inspection and an exact operation ID. It locks
// the full RECORDED inventory, including other stacks' contested resources,
// revalidates after acquiring all locks, then publishes one coherent decision.
// No rollback/no-effects assertion, head deletion or replay is performed.
func (s *State) acknowledge(ctx context.Context, key [32]byte, target, stack, id string) error {
	observed, err := s.inspect(ctx, key, target, stack)
	if err != nil {
		return err
	}
	if observed == nil || observed.OperationID != id || !needsInspection(*observed) {
		return errors.New("inspection acknowledgement does not match unresolved operation")
	}
	locks, err := s.acquire(ctx, keysFor(s, *observed))
	if err != nil {
		return err
	}
	defer release(locks)
	current, err := s.load(nameFor(key), target, stack)
	if err != nil {
		return err
	}
	if current == nil || current.OperationID != id || !needsInspection(*current) {
		return errors.New("operation changed during inspection")
	}
	// If a different operation now owns some reservation, do not touch it. This
	// can happen after partial installation before any effects; ack remains safe
	// because it changes only the exact old operation's immutable resolution.
	current.State = "acknowledged"
	current.Outcome = "unknown"
	current.Effects = "unknown"
	if err = s.publish(*current, true); err != nil {
		return errors.New("inspection acknowledgement not durable")
	}
	return nil
}
func (s *State) Acknowledge(ctx context.Context, target, stack, operationID string) error {
	key, err := s.scope(target, stack)
	if err != nil {
		return err
	}
	return s.acknowledge(ctx, key, target, s.token("stack", stack), operationID)
}
func (s *State) AcknowledgeResource(ctx context.Context, target, kind, name, operationID string) error {
	if !validResourceScope(target, kind, name) {
		return errors.New("invalid resource scope")
	}
	return s.acknowledge(ctx, s.resourceKey(target, Resource{Kind: kind, ID: s.token("resource:"+kind, name)}), target, "", operationID)
}
