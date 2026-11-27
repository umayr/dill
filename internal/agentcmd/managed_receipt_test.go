package agentcmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
)

// Synthetic child output exercises the parser boundary only, not an engine or
// a reported runtime failure. Never substitute these fixtures for target proof.
func TestManagedReceiptBoundary(t *testing.T) {
	owned := []operation.ManagedResource{{Kind: "container", Name: "demo-web", ID: strings.Repeat("a", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("b", 64)}}
	expected := operation.ManagedExpectation{Mode: "update", Resources: owned}
	socket, qualified := "/run/docker.sock", operation.Digest("qualified")
	base := operation.Result{Version: operation.Version, OperationID: "op_" + strings.Repeat("c", 32), Operation: "stop", Target: &operation.Target{Engine: "docker", EndpointDigest: operation.Digest(socket), QualifiedIdentityDigest: qualified}, Stack: "demo", Outcome: "succeeded", Effects: "none", Resources: []operation.Resource{}, Errors: []operation.Error{}, ManagedResources: &owned}
	raw, _ := json.Marshal(base)
	changed := append([]operation.ManagedResource{}, owned...)
	changed[0].ID = strings.Repeat("d", 64)
	changedResult := base
	changedResult.ManagedResources = &changed
	changedRaw, _ := json.Marshal(changedResult)
	failure := base
	failure.Outcome = "failed"
	failure.Effects = "partial"
	failure.ManagedResources = nil
	failure.Errors = []operation.Error{{Code: "ENGINE", Message: "synthetic"}}
	failRaw, _ := json.Marshal(failure)
	reordered := []operation.ManagedResource{owned[1], owned[0]}
	reorderedResult := base
	reorderedResult.ManagedResources = &reordered
	reorderedRaw, _ := json.Marshal(reorderedResult)
	nullError := strings.Replace(strings.Replace(string(failRaw), `"effects":"partial"`, `"effects":"none"`, 1), `"errors":[{"code":"ENGINE","message":"synthetic"}]`, `"errors":[null]`, 1)
	nullResource := strings.Replace(strings.Replace(string(raw), `"effects":"none"`, `"effects":"complete"`, 1), `"resources":[]`, `"resources":[null]`, 1)
	for _, tc := range []struct {
		name, raw     string
		exit          int
		want, effects string
	}{
		{"valid noop", string(raw), 0, "succeeded", "none"},
		{"reordered noop", string(reorderedRaw), 0, "succeeded", "none"},
		{"ownership replacement", string(changedRaw), 0, "unknown", "unknown"},
		{"duplicate effects", strings.Replace(string(failRaw), `"effects":"partial"`, `"effects":"partial","effects":"none"`, 1), 1, "unknown", "unknown"},
		{"case alias effects", strings.Replace(string(failRaw), `"effects":"partial"`, `"effects":"partial","Effects":"none"`, 1), 1, "unknown", "unknown"},
		{"nested alias", strings.Replace(string(raw), `"engine":"docker"`, `"engine":"podman","Engine":"docker"`, 1), 0, "unknown", "unknown"},
		{"null error", nullError, 1, "unknown", "unknown"},
		{"null resource", nullResource, 0, "unknown", "unknown"},
		{"valid partial", string(failRaw), 1, "failed", "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "child")
			entry := filepath.Join(dir, "compose.pkl")
			if e := os.WriteFile(entry, []byte("// synthetic"), 0600); e != nil {
				t.Fatal(e)
			}
			code := "0"
			if tc.exit == 1 {
				code = "1"
			}
			script := "#!/bin/sh\n/bin/cat <<'RECEIPT'\n" + tc.raw + "\nRECEIPT\nexit " + code + "\n"
			if e := os.WriteFile(exe, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			status, got := cliApplyFor("stop", exe, "/pkl", socket, "daemon", "/state")(context.Background(), entry, "demo", operation.Digest("selection"), strings.Repeat("e", 32), qualified, expected)
			if status.State != tc.want || status.Effects != tc.effects {
				t.Fatalf("state=%s effects=%s want=%s", status.State, status.Effects, tc.want)
			}
			if tc.want == "succeeded" && !sameManagedOwnership(owned, got) {
				t.Fatal("successful receipt lost exact ownership")
			}
			if tc.want != "succeeded" && got != nil {
				t.Fatal("ambiguous receipt released ownership")
			}
		})
	}
}
