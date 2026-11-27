package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestBrokerOpenRequiresExistingLockedJournal(t *testing.T) {
	path := historyDirectory(t)
	target := operation.Digest("history")
	if h, e := openDeploymentHistory(path, target, true); e == nil {
		h.dir.Close()
		t.Fatal("missing protected history initialized")
	}
	if _, e := os.Stat(filepath.Join(path, "operations.json")); !os.IsNotExist(e) {
		t.Fatal("protected open created journal", e)
	}
	h, e := openDeploymentHistory(path, target)
	if e != nil {
		t.Fatal(e)
	}
	h.dir.Close()
	if e = os.Remove(filepath.Join(path, "operations.json")); e != nil {
		t.Fatal(e)
	}
	if h, e = openDeploymentHistory(path, target, true); e == nil {
		h.dir.Close()
		t.Fatal("lost protected journal recreated")
	}
}

func TestBrokerOpenRejectsUnidentifiedLegacyJournal(t *testing.T) {
	path := historyDirectory(t)
	target := operation.Digest("history")
	raw, e := canonicalHistory(deploymentHistoryFile{Version: 1, Target: target, Rows: []deploymentRecord{}})
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(path, "operations.json")
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if h, e := openDeploymentHistory(path, target, true); e == nil {
		h.dir.Close()
		t.Fatal("broker startup minted legacy journal identity")
	}
	kept, e := os.ReadFile(file)
	if e != nil || !bytes.Equal(raw, kept) {
		t.Fatal("refused protected history rewritten", e)
	}
	h, e := openDeploymentHistory(path, target)
	if e != nil {
		t.Fatal("ordinary legacy upgrade refused", e)
	}
	defer h.dir.Close()
	if !validApplyID(h.journal) {
		t.Fatal("ordinary history migration lost")
	}
}

func TestBrokerReplayRejectsUnprotectedSuccess(t *testing.T) {
	h, ref, _ := brokerFixture(t)
	id := strings.Repeat("5", 32)
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: strings.Repeat("1", 32), Manifest: strings.TrimPrefix(ref.Scope.SourceDigest, "sha256:"), Broker: &ref}
	if e := h.history.reserve(row, ""); e != nil {
		t.Fatal(e)
	}
	if e := h.history.finish(deploymentStatus{RequestID: id, Stack: "demo", State: "failed", Effects: "none"}, nil); e != nil {
		t.Fatal(e)
	}
	plain := row
	plain.RequestID = strings.Repeat("6", 32)
	plain.Broker = nil
	plain.State = "succeeded"
	plain.Effects = "complete"
	plain.OperationID = "op_" + plain.RequestID
	plain.Owned = []operation.ManagedResource{{Kind: "container", Name: "demo_web", ID: strings.Repeat("d", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("e", 64)}}
	if !validDeploymentRecord(plain) {
		t.Fatal("invalid fixture hides replay boundary")
	}
	// Deliberately corrupt the private fixture on disk, bypassing reserve.
	if e := h.history.save(append(h.history.rows, plain), 0); e != nil {
		t.Fatal(e)
	}
	h.history.dir.Close()
	if reopened, e := openDeploymentHistory(h.history.path, h.history.target); e == nil {
		reopened.dir.Close()
		t.Fatal("replay forgot sticky broker history")
	}
	plain.Mode = "remove"
	plain.Effects = "none"
	plain.Owned = nil
	if validDeploymentRecord(plain) {
		t.Fatal("plain no-effect removal became tombstone")
	}
}

func TestBrokerProtectionSurvivesLifecycleMerge(t *testing.T) {
	h, ref, _ := brokerFixture(t)
	first := strings.Repeat("2", 32)
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: first, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: strings.Repeat("1", 32), Manifest: strings.TrimPrefix(ref.Scope.SourceDigest, "sha256:"), Broker: &ref}
	if e := h.history.reserve(row, ""); e != nil {
		t.Fatal(e)
	}
	if e := h.history.finish(deploymentStatus{RequestID: first, Stack: "demo", State: "failed", Effects: "none"}, nil); e != nil {
		t.Fatal(e)
	}
	if previous, _, plain := h.history.managedPlain("demo"); plain || previous.Broker != nil {
		t.Fatal("plain snapshot selection exposed broker history")
	}
	// Failed/none is skipped by ownership, but broker protection is monotonic.
	plain := row
	plain.RequestID = strings.Repeat("3", 32)
	plain.Broker = nil
	if e := h.history.reserve(plain, ""); e == nil {
		t.Fatal("failed broker attempt allowed plain reservation")
	}
	h.lifecyclePreview = map[string]func(context.Context, string, string) (preflight.Result, error){}
	h.lifecycleApply = map[string]func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource){}
	for _, action := range []string{"start", "stop", "restart"} {
		h.lifecyclePreview[action] = func(context.Context, string, string) (preflight.Result, error) {
			t.Fatal("protected lifecycle reached preview")
			return preflight.Result{}, nil
		}
		h.lifecycleApply[action] = func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
			t.Fatal("protected lifecycle executed")
			return deploymentStatus{}, nil
		}
		routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{action: true, action + "-preview": true})
		for _, preview := range []bool{true, false} {
			path := "/v1/deploy/" + action
			body := map[string]string{"stack": "demo"}
			if preview {
				path += "-preview"
			} else {
				body["selectionDigest"] = operation.Digest("selection")
				body["requestId"] = strings.Repeat("4", 32)
				body["priorRequestId"] = first
				body["confirm"] = action
			}
			raw, _ := json.Marshal(body)
			w := brokerRequest(routes, h.history, "POST", path, "deploy", raw, true)
			if w.Code != 409 || !strings.Contains(w.Body.String(), "broker review required") {
				t.Fatal("protected lifecycle not denied at broker boundary", action, preview, w.Code, w.Body.String())
			}
		}
	}
}
