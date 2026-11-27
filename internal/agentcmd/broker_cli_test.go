package agentcmd

import (
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func brokerScript(t *testing.T, reply brokerCLIReply, exit string) brokerCLI {
	t.Helper()
	raw, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli")
	script := `#!/bin/sh
if [ -n "$DILL_MUST_NOT_INHERIT" ]; then exit 95; fi
case "$*" in *synthetic-private-value*) exit 96;; esac
printf '%s' '` + string(raw) + `'
exit ` + exit + "\n"
	if err = os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return brokerCLI{exe: path, pkl: "/operator/pkl", socket: "/operator/docker.sock", daemon: "daemon", state: "/operator/state", store: "/operator/secrets"}
}
func TestBrokerCLIAdapterPrivateBoundary(t *testing.T) {
	_, ref, _ := brokerFixture(t)
	t.Setenv("DILL_MUST_NOT_INHERIT", "sensitive-placeholder")
	review := brokersecret.Review{Scope: ref.Scope, Handle: ref.Handle, ExpiresAt: time.Now().Add(time.Minute)}
	cli := brokerScript(t, brokerCLIReply{Contract: brokersecret.Version, Prepared: &review}, "0")
	item, _ := brokersecret.NewItem("password", []byte("synthetic-private-value"))
	e := brokersecret.Envelope{Scope: ref.Scope, Items: []brokersecret.Item{item}}
	defer e.Close()
	got, err := cli.prepare(context.Background(), filepath.Join(t.TempDir(), "main.pkl"), e)
	if err != nil || got.Scope != ref.Scope {
		t.Fatal("stdin-only isolated execution failed")
	}
	args := cli.args("broker-prepare", "/trusted/capture/main.pkl", ref.Scope)
	if strings.Contains(strings.Join(args, " "), "synthetic-private-value") {
		t.Fatal("private data in argv")
	}
	wrong := review
	wrong.Scope.SourceDigest = operation.Digest("different")
	cli = brokerScript(t, brokerCLIReply{Contract: brokersecret.Version, Prepared: &wrong}, "0")
	if _, err = cli.prepare(context.Background(), filepath.Join(t.TempDir(), "main.pkl"), e); err == nil {
		t.Fatal("mismatched child scope accepted")
	}
}
func TestBrokerCLIAdapterDoesNotInferSuccessFromExit(t *testing.T) {
	_, ref, _ := brokerFixture(t)
	id := strings.Repeat("1", 32)
	resources := []operation.ManagedResource{{Kind: "network", Name: "demo", ID: strings.Repeat("a", 64)}, {Kind: "container", Name: "demo_web", ID: strings.Repeat("b", 64)}}
	result := operation.Result{Version: operation.Version, OperationID: "op_" + id, Operation: "up", Stack: "demo", Outcome: "succeeded", Effects: "none", Target: &operation.Target{Engine: "docker", EndpointDigest: ref.Scope.EndpointDigest, QualifiedIdentityDigest: ref.Scope.TargetDigest}, Resources: []operation.Resource{}, Errors: []operation.Error{}, ManagedResources: &resources}
	reply := brokerCLIReply{Contract: brokersecret.Version, Reference: &ref, Operation: &result}
	for _, tc := range []struct {
		code  string
		wrong bool
		want  string
	}{{"0", false, "succeeded"}, {"1", false, "unknown"}, {"0", true, "unknown"}} {
		copyResult := result
		if tc.wrong {
			target := *result.Target
			target.EndpointDigest = operation.Digest("other")
			copyResult.Target = &target
		}
		reply.Operation = &copyResult
		cli := brokerScript(t, reply, tc.code)
		got, owned := cli.apply(context.Background(), filepath.Join(t.TempDir(), "main.pkl"), ref, operation.Digest("selection"), id, operation.ManagedExpectation{Mode: "create", Resources: []operation.ManagedResource{}})
		if got.State != tc.want || (got.State == "unknown" && (got.Effects != "unknown" || owned != nil)) {
			t.Fatal("exit/status/evidence mismatch accepted")
		}
	}
}
