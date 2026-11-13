//go:build integration && (linux || darwin)

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// These callers exercise the actual candidate, not the Brine application. They
// deliberately share one private store, including when their stacks differ.
// Keep children in the qualification runner's process group so its cancellation
// cleanup can still terminate make/go/candidate descendants together.
type alphaQualifiedProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output bytes.Buffer
	prompt *alphaQualifiedPrompt
	done   chan struct{}
	err    error // published by closing done; read only after receiving it
	cancel context.CancelFunc
}

type alphaQualifiedPrompt struct {
	mu    sync.Mutex
	tail  []byte
	ready chan struct{}
	once  sync.Once
}

func (p *alphaQualifiedPrompt) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tail = append(p.tail, data...)
	if bytes.Contains(p.tail, []byte("[y/N]")) {
		p.once.Do(func() { close(p.ready) })
	}
	if len(p.tail) > 1024 {
		p.tail = append([]byte(nil), p.tail[len(p.tail)-1024:]...)
	}
	return len(data), nil
}

func startAlphaQualifiedProcess(t *testing.T, f *alphaQualifiedFixture, operation, source string, extra ...string) *alphaQualifiedProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	p := &alphaQualifiedProcess{done: make(chan struct{}), cancel: cancel,
		prompt: &alphaQualifiedPrompt{ready: make(chan struct{})}}
	p.cmd = exec.CommandContext(ctx, f.bin, f.argsAt(f.socket, operation, source, extra...)...)
	p.cmd.WaitDelay = 2 * time.Second
	p.cmd.Stdout, p.cmd.Stderr = &p.output, p.prompt
	var err error
	p.input, err = p.cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal("create qualified caller input failed")
	}
	if err = p.cmd.Start(); err != nil {
		p.input.Close()
		cancel()
		t.Fatalf("start qualified caller failed (%T)", err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.input.Close()
		select {
		case <-p.done:
		default:
			// Only this exact child is signalled. Controlled kill barriers below
			// occur after Pkl evaluation, never while it has evaluator children.
			if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("qualified caller cleanup signal failed (%T)", err)
			}
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Error("qualified caller cleanup did not finish")
			}
		}
		p.cancel()
	})
	return p
}

func (p *alphaQualifiedProcess) waitPrompt(t *testing.T) {
	t.Helper()
	select {
	case <-p.prompt.ready:
		p.assertWaiting(t)
	case <-p.done:
		t.Fatal("qualified caller exited before the attended lock barrier")
	case <-time.After(20 * time.Second):
		t.Fatal("qualified caller did not reach the attended lock barrier")
	}
}

func (p *alphaQualifiedProcess) assertWaiting(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		t.Fatal("the first caller no longer holds its active operation")
	default:
	}
}

func (p *alphaQualifiedProcess) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		t.Fatal("qualified caller did not return after its controlled barrier")
	}
	if p.err == nil {
		return 0
	}
	if exited, ok := p.err.(*exec.ExitError); ok {
		return exited.ExitCode()
	}
	t.Fatalf("qualified caller did not provide a process exit (%T)", p.err)
	return -1
}

func (p *alphaQualifiedProcess) decline(t *testing.T, f *alphaQualifiedFixture) {
	t.Helper()
	if _, err := io.WriteString(p.input, "n\n"); err != nil {
		t.Fatalf("decline attended caller failed (%T)", err)
	}
	p.input.Close()
	if code := p.wait(t); code != 0 {
		t.Fatalf("declined attended caller exit=%d, want 0", code)
	}
	result, err := decodeAlphaOperation(p.output.Bytes())
	if err != nil || result.Operation != "rm" || result.Outcome != "cancelled" || result.Effects != "none" || len(result.Errors) != 0 || len(result.Resources) == 0 {
		t.Fatal("declined caller did not retain its no-effect captured-intent receipt")
	}
	if result.Target == nil || result.Target.QualifiedIdentityDigest != alphaDigest("dill-local-docker-v1\x00"+f.daemon) || result.Stack != f.stack || f.seen[result.OperationID] {
		t.Fatal("declined caller lost its qualified target or operation correlation")
	}
	f.seen[result.OperationID] = true
}

func alphaQualifiedSucceeded(t *testing.T, f *alphaQualifiedFixture, operation, source string) alphaOperationResult {
	t.Helper()
	result, code := f.run(t, operation, source, "")
	if code != 0 || result.Outcome != "succeeded" || result.Effects != "complete" || len(result.Errors) != 0 || result.Target == nil || result.Target.QualifiedIdentityDigest == "" {
		t.Fatalf("qualified %s did not complete with an attested receipt", operation)
	}
	return result
}

func alphaQualifiedContention(t *testing.T, aliases bool) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	alphaQualifiedSucceeded(t, f, "stop", "cli")
	before := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_app")
	first := startAlphaQualifiedProcess(t, f, "rm", "cli")
	first.waitPrompt(t)

	socket := f.socket
	extra := []string{"--timeout", "2s"}
	if aliases {
		alias := filepath.Join(t.TempDir(), "daemon.sock")
		if err := os.Symlink(strings.TrimPrefix(f.socket, "unix://"), alias); err != nil {
			t.Fatal(err)
		}
		socket = "unix://" + alias
		cfgAlias, stateAlias := filepath.Join(t.TempDir(), "compose.pkl"), filepath.Join(t.TempDir(), "state")
		if err := os.Symlink(f.cfg, cfgAlias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.state, stateAlias); err != nil {
			t.Fatal(err)
		}
		extra = append(extra, "-f", cfgAlias, "--coordination-state-dir", stateAlias)
	}
	inventory := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	second, code := f.runAt(t, socket, "start", "brine", "", extra...)
	until := time.Now().UTC().Format(time.RFC3339Nano)
	if code != 1 || second.Outcome != "cancelled" || second.Effects != "none" || len(second.Errors) != 1 || second.Errors[0].Code != "CANCELLED" || second.Target == nil || second.Target.QualifiedIdentityDigest == "" {
		t.Fatal("contending native caller did not cancel at the qualified lock without effects")
	}
	if events := runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" || alphaResourceSnapshot(t, "docker") != inventory {
		t.Fatal("contending caller mutated the daemon or stole the active lock")
	}
	first.assertWaiting(t)
	first.decline(t, f)
	if after := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_app"); after != before {
		t.Fatal("declined lock holder changed the intended resource")
	}
	alphaQualifiedSucceeded(t, f, "start", "brine")
	f.assertForeign(t)
}

func TestAlphaQualifiedSameStackConcurrency(t *testing.T) { alphaQualifiedContention(t, false) }
func TestAlphaQualifiedAliasConcurrency(t *testing.T)     { alphaQualifiedContention(t, true) }

func TestAlphaQualifiedDifferentStackConcurrency(t *testing.T) {
	first, other := newAlphaQualifiedFixture(t), newAlphaQualifiedFixture(t)
	other.state, other.seen = first.state, first.seen // SAME store and actual target
	first.seed(t)
	other.seed(t)
	alphaQualifiedSucceeded(t, first, "stop", "cli")
	alphaQualifiedSucceeded(t, other, "stop", "brine")
	actor := startAlphaQualifiedProcess(t, first, "rm", "cli")
	actor.waitPrompt(t)
	alphaQualifiedSucceeded(t, other, "start", "brine")
	actor.assertWaiting(t)
	actor.decline(t, first)
	first.assertForeign(t)
	other.assertForeign(t)
}

type alphaCoordinationRecord struct {
	Version     string                       `json:"version"`
	OperationID string                       `json:"operationId"`
	Source      string                       `json:"source"`
	Target      string                       `json:"target"`
	Stack       string                       `json:"stack"`
	Resources   []*alphaCoordinationResource `json:"resources"`
	State       string                       `json:"state"`
	Outcome     string                       `json:"outcome"`
	Effects     string                       `json:"effects"`
}

type alphaCoordinationResource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Action string `json:"action"`
}

func decodeAlphaCoordinationInspection(data []byte) (alphaCoordinationRecord, error) {
	var envelope struct {
		Version string                   `json:"version"`
		Record  *alphaCoordinationRecord `json:"record"`
		Error   *string                  `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return alphaCoordinationRecord{}, errors.New("invalid coordination envelope")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return alphaCoordinationRecord{}, errors.New("trailing coordination output")
	}
	if envelope.Version != "dill.coordination/1" || envelope.Record == nil || envelope.Error == nil || *envelope.Error != "" {
		return alphaCoordinationRecord{}, errors.New("unavailable coordination record")
	}
	r := envelope.Record
	validFingerprint := func(s string) bool {
		raw, err := hex.DecodeString(s)
		return err == nil && len(raw) == 32 && strings.ToLower(s) == s
	}
	id, err := hex.DecodeString(strings.TrimPrefix(r.OperationID, "op_"))
	if r.Version != "dill.coordination/1" || !strings.HasPrefix(r.OperationID, "op_") || err != nil || len(id) != 16 || (r.Source != "cli" && r.Source != "brine") || !validFingerprint(r.Target) || !validFingerprint(r.Stack) || len(r.Resources) == 0 || r.State == "" || r.Outcome == "" || r.Effects == "" {
		return alphaCoordinationRecord{}, errors.New("incomplete coordination record")
	}
	for _, resource := range r.Resources {
		if resource == nil || !validFingerprint(resource.ID) || resource.Action == "" || (resource.Kind != "container" && resource.Kind != "network" && resource.Kind != "volume") {
			return alphaCoordinationRecord{}, errors.New("invalid coordination resource")
		}
	}
	return *r, nil
}

func alphaOfflineCoordination(t *testing.T, f *alphaQualifiedFixture, action string, extra ...string) ([]byte, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"coordination", action, "--state-dir", f.state, "--daemon-id", f.daemon, "--stack", f.stack}
	cmd := exec.CommandContext(ctx, f.bin, append(args, extra...)...)
	data, err := cmd.Output()
	if err == nil {
		return data, 0
	}
	if exited, ok := err.(*exec.ExitError); ok {
		return data, exited.ExitCode()
	}
	t.Fatalf("offline recovery did not return an exit (%T)", err)
	return nil, -1
}

func alphaInspectCoordination(t *testing.T, f *alphaQualifiedFixture) alphaCoordinationRecord {
	t.Helper()
	data, code := alphaOfflineCoordination(t, f, "inspect")
	record, err := decodeAlphaCoordinationInspection(data)
	if code != 0 || err != nil {
		t.Fatal("offline inspection did not expose the recorded operation envelope")
	}
	return record
}

func alphaOfflineNoEngineEffects(t *testing.T, f *alphaQualifiedFixture, fn func()) {
	t.Helper()
	before := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	fn()
	until := time.Now().UTC().Format(time.RFC3339Nano)
	if events := runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" || alphaResourceSnapshot(t, "docker") != before {
		t.Fatal("offline inspect/acknowledge executed, replayed, or rolled back engine work")
	}
	f.assertForeign(t)
}

const alphaQualifiedReadinessBarrier = `volumes = List("data:/data")
healthcheck { test = "false"; interval = 300.s; timeout = 1.s; retries = 3; startPeriod = 0.s }`

func TestAlphaQualifiedReadinessFixtureEvaluation(t *testing.T) {
	root, cfg := repoRoot(t), filepath.Join(t.TempDir(), "compose.pkl")
	writeAlphaPreparedConfig(t, cfg, root, "docker", "synthetic-crash-barrier", alphaPreparedImage, alphaQualifiedReadinessBarrier, false)
	evaluateAlphaFixture(t, cfg)
}

func TestAlphaQualifiedCrashUnknownRecovery(t *testing.T) {
	f, other := newAlphaQualifiedFixture(t), newAlphaQualifiedFixture(t)
	other.state, other.seen = f.state, f.seen
	other.seed(t)
	alphaQualifiedSucceeded(t, other, "stop", "brine")
	// The first check is five minutes away. Readiness stays 'starting' without
	// background health executions/events racing our no-write assertions.
	f.write(t, f.image, alphaQualifiedReadinessBarrier, false)
	actor := startAlphaQualifiedProcess(t, f, "up", "cli")
	var original string
	deadline := time.After(20 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
waitEffect:
	for {
		select {
		case <-actor.done:
			t.Fatal("candidate completed before the after-effect crash barrier")
		case <-deadline:
			t.Fatal("candidate did not create a running readiness-barrier workload")
		case <-ticker.C:
			data, err := runEngineRaw(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Running}}|{{.State.Health.Status}}", f.stack+"_app")
			parts := strings.Split(strings.TrimSpace(data), "|")
			if err == nil && len(parts) == 3 && parts[1] == "true" && parts[2] == "starting" {
				original = parts[0]
				break waitEffect
			}
		}
	}
	actor.assertWaiting(t)
	if err := actor.cmd.Process.Kill(); err != nil {
		t.Fatal("failed to inject the controlled after-effect CLI death")
	}
	if code := actor.wait(t); code != -1 {
		t.Fatalf("crash injection exit=%d, want signal termination", code)
	}
	if exited, ok := actor.err.(*exec.ExitError); !ok || !exited.ProcessState.Sys().(syscall.WaitStatus).Signaled() || exited.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("after-effect caller was not killed by the intended signal")
	}
	if _, err := decodeAlphaOperation(actor.output.Bytes()); err == nil {
		t.Fatal("after-effect killed caller unexpectedly emitted a terminal receipt")
	}
	record := alphaInspectCoordination(t, f)
	if record.State != "in_progress" || record.Outcome != "unknown" || record.Effects != "unknown" || record.Source != "cli" {
		t.Fatal("process death did not retain the actual caller's unknown operation")
	}
	f.seen[record.OperationID] = true
	blocked := f.rejectNoWrites(t, "stop", 1, "failed")
	if len(blocked.Errors) != 1 || blocked.Errors[0].Code != "COORDINATION_INSPECTION_REQUIRED" {
		t.Fatal("unknown record did not require explicit inspection")
	}
	// A different stack in the SAME store may still perform a real mutation.
	alphaQualifiedSucceeded(t, other, "start", "brine")
	if after := alphaInspectCoordination(t, f); after.OperationID != record.OperationID || after.Outcome != "unknown" {
		t.Fatal("different-stack work altered the unresolved operation")
	}
	wrong := "op_" + strings.Repeat("0", 32)
	if wrong == record.OperationID {
		wrong = "op_" + strings.Repeat("1", 32)
	}
	alphaOfflineNoEngineEffects(t, f, func() {
		if _, code := alphaOfflineCoordination(t, f, "acknowledge", "--operation-id", wrong, "--inspected"); code != 1 {
			t.Fatalf("wrong-operation acknowledgement exit=%d, want 1", code)
		}
		if _, code := alphaOfflineCoordination(t, f, "acknowledge", "--operation-id", record.OperationID); code != 2 {
			t.Fatalf("uninspected acknowledgement exit=%d, want 2", code)
		}
	})
	f.rejectNoWrites(t, "stop", 1, "failed")
	// Actual operator-style read-only resource inspection precedes consent.
	if inspected := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Running}}", f.stack+"_app"); inspected != original+"|true" {
		t.Fatal("actual unknown resource was not available for inspection")
	}
	alphaOfflineNoEngineEffects(t, f, func() {
		if _, code := alphaOfflineCoordination(t, f, "acknowledge", "--operation-id", record.OperationID, "--inspected"); code != 0 {
			t.Fatalf("exact inspected acknowledgement exit=%d, want 0", code)
		}
	})
	if acknowledged := alphaInspectCoordination(t, f); acknowledged.OperationID != record.OperationID || acknowledged.State != "acknowledged" {
		t.Fatal("exact consent did not publish the inspected operation acknowledgement")
	}
	if unchanged := runEngine(t, "docker", "inspect", "--format", "{{.Id}}", f.stack+"_app"); unchanged != original {
		t.Fatal("acknowledgement replayed the original operation")
	}
	f.write(t, f.image, `volumes = List("data:/data")`, false)
	next := alphaQualifiedSucceeded(t, f, "up", "brine")
	if terminal := alphaInspectCoordination(t, f); terminal.OperationID != next.OperationID || terminal.Source != "brine" || terminal.State != "terminal" || terminal.Outcome != "succeeded" || terminal.Effects != "complete" {
		t.Fatal("fresh caller receipt and durable terminal record lost their correlation")
	}
	if next.OperationID == record.OperationID || runEngine(t, "docker", "inspect", "--format", "{{.Id}}", f.stack+"_app") == original {
		t.Fatal("fresh operation did not independently reconcile the inspected state")
	}
	f.assertForeign(t)
	other.assertForeign(t)
}

func alphaPrivateStateSnapshot(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(&out, "directory-mode:%o\n", info.Mode().Perm())
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".lock") {
			continue // Lock descriptors are coordination, not a repair operation.
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal("read private synthetic state failed")
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&out, "%s:%s\n", entry.Name(), hex.EncodeToString(digest[:]))
	}
	return out.String()
}

func TestAlphaQualifiedCorruptJournal(t *testing.T) {
	for _, kind := range []string{"truncated-head", "missing-key", "unsafe-permissions"} {
		t.Run(kind, func(t *testing.T) {
			f := newAlphaQualifiedFixture(t)
			f.seed(t)
			switch kind {
			case "truncated-head":
				entries, err := os.ReadDir(f.state)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".json") {
						if err := os.WriteFile(filepath.Join(f.state, entry.Name()), []byte("{"), 0o600); err != nil {
							t.Fatal(err)
						}
						count++
					}
				}
				if count == 0 {
					t.Fatal("corruption fixture found no real recorded heads")
				}
			case "missing-key":
				if err := os.Remove(filepath.Join(f.state, "identity.key")); err != nil {
					t.Fatal(err)
				}
			case "unsafe-permissions":
				if err := os.Chmod(f.state, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(f.state, 0o700); err != nil {
						t.Errorf("restore private fixture mode failed (%T)", err)
					}
				})
			}
			before := alphaPrivateStateSnapshot(t, f.state)
			f.rejectNoWrites(t, "stop", 1, "failed")
			alphaOfflineNoEngineEffects(t, f, func() {
				if _, code := alphaOfflineCoordination(t, f, "inspect"); code != 1 {
					t.Fatalf("unsafe/corrupt journal inspect exit=%d, want 1", code)
				}
			})
			if after := alphaPrivateStateSnapshot(t, f.state); after != before {
				t.Fatal("rejection silently repaired or reinitialized private state")
			}
			f.assertForeign(t)
		})
	}
}

func TestAlphaQualifiedRecoveryDecoder(t *testing.T) {
	record := alphaCoordinationRecord{Version: "dill.coordination/1", OperationID: "op_" + strings.Repeat("0", 32), Source: "cli", Target: strings.Repeat("a", 64), Stack: strings.Repeat("b", 64), Resources: []*alphaCoordinationResource{{Kind: "container", ID: strings.Repeat("c", 64), Action: "up"}}, State: "in_progress", Outcome: "unknown", Effects: "unknown"}
	recordJSON, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": "dill.coordination/1", "record": record, "error": ""})
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := decodeAlphaCoordinationInspection(data); err != nil || parsed.OperationID != record.OperationID {
		t.Fatal("valid actual CLI inspection envelope rejected")
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"null-record", `"record":` + string(recordJSON), `"record":null`},
		{"missing-record", `"record":` + string(recordJSON) + `,`, ""},
		{"null-error", `"error":""`, `"error":null`},
		{"missing-error", `"error":"",`, ""},
		{"unknown-field", `"error":""`, `"error":"","rawEngineError":"synthetic"`},
		{"null-resource", `"resources":[{`, `"resources":[null,{`},
		{"wrong-version", `dill.coordination/1`, `dill.coordination/999`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := bytes.ReplaceAll(data, []byte(tc.old), []byte(tc.replacement))
			if bytes.Equal(data, bad) {
				t.Fatal("negative decoder fixture did not change")
			}
			if _, err := decodeAlphaCoordinationInspection(bad); err == nil {
				t.Fatal("invalid inspection envelope accepted")
			}
		})
	}
	if _, err := decodeAlphaCoordinationInspection(append(append([]byte(nil), data...), []byte(`{}`)...)); err == nil {
		t.Fatal("trailing inspection object accepted")
	}
	if _, err := decodeAlphaCoordinationInspection(recordJSON); err == nil {
		t.Fatal("raw record accepted in place of actual CLI envelope")
	}
}

// Pure process/pipe verification. This does not opt in to a daemon or count as
// runtime qualification, and may be run safely on developer machines.
func TestAlphaQualifiedProcessHelpers(t *testing.T) {
	t.Run("actual-attended-argument-contract", func(t *testing.T) {
		root := t.TempDir()
		f := &alphaQualifiedFixture{bin: dillBinary(t, root), cfg: filepath.Join(root, "absent.pkl"),
			stack: "argument-contract", state: filepath.Join(root, "absent-state"), daemon: "synthetic-daemon"}
		for _, tc := range []struct {
			name, want string
			extra      []string
		}{
			{"attended-is-default", "UNSUPPORTED_FORMAT", nil},
			{"invented-flag-rejected", "INVALID_ARGUMENTS", []string{"--attended"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				// A deliberately unsupported format is checked AFTER flag parsing
				// but BEFORE config loading, coordination, socket or engine access.
				// This exercises the actual candidate parser, not the shell helper.
				args := f.argsAt(filepath.Join(root, "absent.sock"), "rm", "cli", tc.extra...)
				args = append(args, "--format", "unsupported-test-sentinel")
				data, err := exec.Command(f.bin, args...).Output()
				exited, ok := err.(*exec.ExitError)
				if !ok || exited.ExitCode() != 2 {
					t.Fatal("offline argument-contract sentinel did not return exit 2")
				}
				result, err := decodeAlphaOperation(data)
				if err != nil || result.Outcome != "invalid_input" || result.Effects != "none" || len(result.Errors) != 1 || result.Errors[0].Code != tc.want {
					t.Fatal("actual attended argument-contract classification mismatch")
				}
			})
		}
		if _, err := os.Stat(f.state); !os.IsNotExist(err) {
			t.Fatal("argument-contract sentinel touched private coordination state")
		}
	})
	for _, kind := range []string{"decline", "kill"} {
		t.Run(kind, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "caller")
			body := "#!/bin/sh\nprintf 'Continue? [y/N] ' >&2\nread answer\nprintf '{\"reply\":\"%s\"}\\n' \"$answer\"\n"
			if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			f := &alphaQualifiedFixture{bin: script}
			p := startAlphaQualifiedProcess(t, f, "rm", "cli")
			p.waitPrompt(t)
			p.assertWaiting(t)
			if kind == "kill" {
				if err := p.cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				if code := p.wait(t); code != -1 {
					t.Fatalf("killed helper exit=%d", code)
				}
			} else {
				if _, err := io.WriteString(p.input, "n\n"); err != nil {
					t.Fatal(err)
				}
				p.input.Close()
				if code := p.wait(t); code != 0 || strings.TrimSpace(p.output.String()) != `{"reply":"n"}` {
					t.Fatal("controlled helper did not return its own input")
				}
			}
		})
	}
}
