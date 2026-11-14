//go:build integration && (linux || darwin)

package integration

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"

	"github.com/umayr/dill/internal/preflight"
)

// This case is gated by the existing independently provisioned native fixture.
// It must run only on the trusted disposable qualification host, never as a
// substitute for actual Brine application/source-policy qualification.
func (f *alphaQualifiedFixture) namedReview(t *testing.T, action string, extra ...string) preflight.Result {
	t.Helper()
	args := []string{"preflight", action, "--contract", preflight.Version, "--format", "json", "--engine", "docker", "--engine-socket", f.socket, "-p", f.stack, "-f", f.cfg, "--coordination-state-dir", f.state, "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", f.daemon, "--coordination-source", "cli", "--timeout", "60s"}
	args = append(args, extra...)
	before := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, f.bin, args...).Output()
	until := time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		t.Fatalf("named review failed: %T", err)
	}
	var result preflight.Result
	if json.Unmarshal(data, &result) != nil || result.Validate(0) != nil || result.Operation != action || result.Stack != f.stack || f.seen[result.OperationID] {
		t.Fatal("invalid named review envelope")
	}
	f.seen[result.OperationID] = true
	if events := runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" {
		t.Fatal("named review produced daemon mutation events")
	}
	if after := alphaResourceSnapshot(t, "docker"); after != before {
		t.Fatal("named review changed complete synthetic resource inventory/state")
	}
	f.assertForeign(t)
	return result
}

func TestAlphaQualifiedNamedPreflightFiveRoutes(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		extra := []string{}
		if action == "up" {
			extra = []string{"--no-prune", "--pull", "never"}
		}
		review := f.namedReview(t, action, extra...)
		args := append(append([]string{}, extra...), "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
		result, code := f.run(t, action, "cli", "", args...)
		if action == "down" {
			if code != 1 || result.Outcome != "failed" || result.Effects != "partial" {
				t.Fatal("retained-network down axes changed")
			}
		} else if code != 0 || result.Outcome != "succeeded" {
			t.Fatalf("bound %s failed with exit %d", action, code)
		}
	}
}

func TestAlphaQualifiedNamedPreflightMismatchBeforeEnsure(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	review := f.namedReview(t, "up", "--no-prune", "--pull", "never")
	result := f.rejectNoWrites(t, "up", 1, "failed", "--no-prune", "--pull", "never", "--force-recreate", "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
	if len(result.Errors) != 1 || result.Errors[0].Code != "PREFLIGHT_MISMATCH" {
		t.Fatal("changed effective input was not rejected before engine writes")
	}
}
