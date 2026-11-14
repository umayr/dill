//go:build integration && (linux || darwin)

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/preflight"
)

// Independently check exact object/row membership, duplicates, bounds and EOF.
// Production validation is additional, never the only wire oracle.
func decodeAlphaNamed(data []byte, code int) (preflight.Result, error) {
	var result preflight.Result
	if len(data) > 262144 {
		return result, fmt.Errorf("named output exceeds frozen bound")
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("excessive JSON depth")
		}
		token, err := tokens.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for tokens.More() {
				key, err := tokens.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for tokens.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = tokens.Token()
		return err
	}
	if err := visit(0); err != nil {
		return result, err
	}
	if _, err := tokens.Token(); err != io.EOF {
		return result, fmt.Errorf("trailing JSON")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || len(root) != 14 {
		return result, fmt.Errorf("named root is not exactly 14 fields")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := result.Validate(code); err != nil {
		return result, err
	}
	if result.Version != "dill.preflight/1" || result.EngineEffects != "none" || result.ResourcesTruncated || len(result.Resources) > 256 {
		return result, fmt.Errorf("named frozen envelope mismatch")
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(root["resources"], &rows); err != nil {
		return result, err
	}
	for _, row := range rows {
		if len(row) != 7 {
			return result, fmt.Errorf("resource is not exactly seven fields")
		}
	}
	if code != 0 && (result.InputDigest != nil || result.SelectionDigest != nil || len(result.Resources) != 0 || result.Coverage.Containers.Complete || result.Coverage.Network.Complete || result.Coverage.Volumes.Complete) {
		return result, fmt.Errorf("failed preview retained positive evidence")
	}
	return result, nil
}

// Both positive and negative previews use actual candidate processes and an
// independent daemon event + complete inventory window. An outer kill is FAIL,
// not evidence of the native --timeout mechanism or a fabricated terminal ID.
func (f *alphaQualifiedFixture) namedAttempt(t *testing.T, action, timeout string, extra ...string) (preflight.Result, int) {
	t.Helper()
	args := []string{"preflight", action, "--contract", "dill.preflight/1", "--format", "json", "--engine", "docker", "--engine-socket", f.socket, "-p", f.stack, "-f", f.cfg, "--coordination-state-dir", f.state, "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", f.daemon, "--coordination-source", "cli", "--timeout", timeout}
	args = append(args, extra...)
	before := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, f.bin, args...).Output()
	until := time.Now().UTC().Format(time.RFC3339Nano)
	code := 0
	if err != nil {
		exited, ok := err.(*exec.ExitError)
		if !ok || exited.ExitCode() < 0 || ctx.Err() != nil {
			t.Fatalf("preview did not return a native structured terminal (%T)", err)
		}
		code = exited.ExitCode()
	}
	result, err := decodeAlphaNamed(data, code)
	if err != nil || result.Operation != action || result.Stack != f.stack || f.seen[result.OperationID] {
		t.Fatalf("invalid named terminal, raw output withheld: %v", err)
	}
	f.seen[result.OperationID] = true
	if result.Target != nil && (result.Target.Engine != "docker" || result.Target.EndpointDigest != alphaDigest(f.socket) || result.Target.QualifiedIdentityDigest != alphaDigest("dill-local-docker-v1\x00"+f.daemon)) {
		t.Fatal("named target differs from explicit independently provisioned target")
	}
	if events := runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" || alphaResourceSnapshot(t, "docker") != before {
		t.Fatal("preview wrote daemon events or changed full inventory")
	}
	f.assertForeign(t)
	return result, code
}

func alphaNamedMismatch(t *testing.T, f *alphaQualifiedFixture, digest string, extra ...string) {
	t.Helper()
	args := append([]string{"--no-prune", "--pull", "never", "--preflight-contract", "dill.preflight/1", "--expect-preflight", digest}, extra...)
	result := f.rejectNoWrites(t, "up", 1, "failed", args...)
	if len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
		t.Fatal("stale selection was not compared and refused before ALL mutators")
	}
}

func alphaNamedData(t *testing.T, f *alphaQualifiedFixture) {
	t.Helper()
	reader := f.stack + "-named-reader"
	t.Cleanup(func() { alphaCleanupResource(t, "docker", "container", reader) })
	if value := runEngine(t, "docker", "run", "--rm", "--name", reader, "--network", "none", "--read-only", "-v", f.stack+"_data:/data:ro", f.imageID, "cat", "/data/value"); value != "synthetic-named-v2" {
		t.Fatal("named data changed through lifecycle/down retention")
	}
	f.assertForeign(t)
}

func alphaCopyNamedState(t *testing.T, source string) string {
	t.Helper()
	destination, err := os.MkdirTemp(filepath.Dir(source), "named-copy-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			if err := os.RemoveAll(destination); err != nil {
				t.Error("copied private state cleanup failed")
			}
		}
	})
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	var originals, copies []os.FileInfo
	var paths []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !entry.Type().IsRegular() {
			t.Fatal("unexpected state copy entry")
		}
		target := filepath.Join(destination, entry.Name())
		linked := false
		for i, prior := range originals {
			if os.SameFile(info, prior) {
				if err := os.Link(paths[i], target); err != nil {
					t.Fatal(err)
				}
				linked = true
				break
			}
		}
		if !linked {
			data, err := os.ReadFile(filepath.Join(source, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
				t.Fatal(err)
			}
		}
		copied, err := os.Stat(target)
		if err != nil || os.SameFile(info, copied) {
			t.Fatal("copied state shares an inode with original")
		}
		originals = append(originals, info)
		copies = append(copies, copied)
		paths = append(paths, target)
	}
	for i := range originals {
		for j := range originals {
			if os.SameFile(originals[i], originals[j]) != os.SameFile(copies[i], copies[j]) {
				t.Fatal("state copy did not preserve internal hardlink topology")
			}
		}
	}
	return destination
}

func TestAlphaQualifiedNamedPreflightStoreSelectorBinding(t *testing.T) {
	for _, kind := range []string{"state-alias", "socket-alias", "copied-state"} {
		t.Run(kind, func(t *testing.T) {
			f := newAlphaQualifiedFixture(t)
			f.seed(t)
			original := f.namedReview(t, "up", "--no-prune", "--pull", "never")
			repeat := f.namedReview(t, "up", "--no-prune", "--pull", "never")
			if *original.SelectionDigest != *repeat.SelectionDigest {
				t.Fatal("preview's own journal metadata changed the selection")
			}
			alias := *f
			switch kind {
			case "state-alias":
				alias.state = filepath.Join(t.TempDir(), "state")
				if err := os.Symlink(f.state, alias.state); err != nil {
					t.Fatal(err)
				}
			case "socket-alias":
				path := filepath.Join(t.TempDir(), "socket")
				if err := os.Symlink(strings.TrimPrefix(f.socket, "unix://"), path); err != nil {
					t.Fatal(err)
				}
				alias.socket = "unix://" + path
			case "copied-state":
				alias.state = alphaCopyNamedState(t, f.state)
			}
			// A positive destination preview proves copied metadata is genuinely usable.
			fresh := alias.namedReview(t, "up", "--no-prune", "--pull", "never")
			if kind == "state-alias" {
				if *fresh.SelectionDigest != *original.SelectionDigest {
					t.Fatal("same retained store alias changed selection")
				}
				result, code := alias.run(t, "up", "cli", "", "--no-prune", "--pull", "never", "--preflight-contract", "dill.preflight/1", "--expect-preflight", *original.SelectionDigest)
				alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
			} else {
				if *fresh.SelectionDigest == *original.SelectionDigest {
					t.Fatal("changed store inode or socket selector did not bind selection")
				}
				alphaNamedMismatch(t, &alias, *original.SelectionDigest)
			}
		})
	}
}

func TestAlphaQualifiedNamedPreflightLockCancellation(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	holder := startAlphaQualifiedProcess(t, f, "rm", "cli")
	holder.waitPrompt(t)
	review, code := f.namedAttempt(t, "stop", "2s")
	if code != 1 || review.Outcome != "cancelled" || review.Target == nil || len(review.Errors) != 1 || review.Errors[0].Code != "CANCELLED" {
		t.Fatal("native held-lock deadline did not yield structured cancellation")
	}
	holder.assertWaiting(t)
	holder.decline(t, f)
	record := alphaInspectCoordination(t, f)
	if record.OperationID == review.OperationID || !f.seen[record.OperationID] || record.State != "terminal" || record.Outcome != "cancelled" || record.Effects != "none" {
		t.Fatal("failed lock preview invented/replaced the actual holder's durable terminal")
	}
	f.namedReview(t, "stop") // Fresh explicit request, never a replay of cancellation.
}

func TestAlphaQualifiedNamedPreflightEmptyAndOrphans(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		f := newAlphaQualifiedFixture(t)
		review := f.namedReview(t, "start")
		result, code := f.run(t, "start", "cli", "", "--preflight-contract", "dill.preflight/1", "--expect-preflight", *review.SelectionDigest)
		alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
		if value := runEngine(t, "docker", "ps", "-aq", "--filter", "label=dill.stack="+f.stack); value != "" {
			t.Fatal("empty start invented a container")
		}
		runEngine(t, "docker", "network", "inspect", f.stack)
		down := f.namedReview(t, "down")
		result, code = f.run(t, "down", "cli", "", "--preflight-contract", "dill.preflight/1", "--expect-preflight", *down.SelectionDigest)
		alphaQualifiedAxes(t, result, code, 1, "failed", "none")
		f.namedReview(t, "stop") // failed/none is NOT an inspection barrier.
	})
	t.Run("orphan", func(t *testing.T) {
		f := newAlphaQualifiedFixture(t)
		f.write(t, f.image, `volumes = List("data:/data")`, true)
		f.seed(t)
		f.write(t, f.image, `volumes = List("data:/data")`, false)
		result, code := f.namedAttempt(t, "up", "60s", "--no-prune", "--pull", "never")
		if code != 1 || len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_UNAVAILABLE" {
			t.Fatal("nonempty orphan selection was not refused")
		}
		runEngine(t, "docker", "inspect", f.stack+"_other")
	})
}

func TestAlphaQualifiedNamedPreflightMetadataRefusal(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	f.namedReview(t, "stop")
	if err := os.Chmod(f.state, 0755); err != nil {
		t.Fatal(err)
	}
	result, code := f.namedAttempt(t, "stop", "60s")
	if code != 1 || len(result.Errors) == 0 || result.Errors[0].Code != "COORDINATION_UNAVAILABLE" {
		t.Fatal("unsafe state metadata was not refused")
	}
	info, err := os.Stat(f.state)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("preview silently repaired state mode")
	}
	if err := os.Chmod(f.state, 0700); err != nil {
		t.Fatal(err)
	} // Fixture restoration, not product auto-repair.
	f.namedReview(t, "stop")
}

// Pure wire regression, not native evidence. No engine or candidate invocation.
func TestAlphaNamedQualificationDecoder(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/preflight/testdata/preview-up-create.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Goldens carry an explicit design-only marker, outside the wire object.
	var fixture struct {
		Result json.RawMessage `json:"stdout"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Result) == 0 {
		fixture.Result = data
	}
	if _, err := decodeAlphaNamed(fixture.Result, 0); err != nil {
		t.Fatalf("frozen positive failed: %v", err)
	}
	bad := [][]byte{append(append([]byte{}, fixture.Result...), []byte(" {}")...), bytes.Replace(fixture.Result, []byte(`"version":`), []byte(`"version":"duplicate","version":`), 1)}
	for _, data := range bad {
		if _, err := decodeAlphaNamed(data, 0); err == nil {
			t.Fatal("duplicate or trailing wire accepted")
		}
	}
}
