package agentcmd

import (
	"context"
	"fmt"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckedApplyRequiresCurrentPredecessorAndNeverFallsBack(t *testing.T) {
	history, e := openDeploymentHistory(historyDirectory(t), operation.Digest("target"))
	if e != nil {
		t.Fatal(e)
	}
	defer history.dir.Close()
	prior, id := strings.Repeat("1", 32), strings.Repeat("2", 32)
	manifest := strings.Repeat("a", 64)
	selection := "sha256:" + strings.Repeat("b", 64)
	owned := []operation.ManagedResource{{Kind: "container", Name: "demo-web", ID: strings.Repeat("c", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("d", 64)}}
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: prior, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: prior, Manifest: manifest}
	if history.reserve(row, "") != nil || history.finish(deploymentStatus{RequestID: prior, Stack: "demo", State: "succeeded", Effects: "complete", OperationID: "op_" + prior}, owned) != nil {
		t.Fatal("seed")
	}
	previews, calls := 0, 0
	endpoint := &applyEndpoint{history: history, slot: make(chan struct{}, 1), previewEndpoint: previewEndpoint{stages: stageGate(func(_ context.Context, _, _ string, f func(string) error) error { return f("/trusted/entry.pkl") }), preview: func(context.Context, string, string) (preflight.Result, error) {
		previews++
		resources := []preflight.Resource{}
		for _, r := range owned {
			resources = append(resources, preflight.Resource{Kind: r.Kind, Name: r.Name, Identity: r.ID, IdentityKind: "immutable-id", Presence: "present"})
		}
		return preflight.Result{Target: &preflight.Target{}, SelectionDigest: &selection, Resources: resources}, nil
	}}, apply: func(_ context.Context, _, stack, _, request, _ string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
		calls++
		if expected.Mode != "update" {
			t.Fatal("checked apply became create")
		}
		return deploymentStatus{RequestID: request, Stack: stack, State: "succeeded", Effects: "complete", OperationID: "op_" + request}, owned
	}}
	handler := authenticatedHTTPS(nil, nil, nil, endpoint, "monitor", "deploy", httpsDeployGrants{"checked-apply": true})
	send := func(token, journal, previous, request string) int {
		body := fmt.Sprintf(`{"stageId":%q,"manifestSha256":%q,"stack":"demo","selectionDigest":%q,"requestId":%q,"priorRequestId":%q,"confirm":"update"}`, prior, manifest, selection, request, previous)
		r := httptest.NewRequest("POST", "https://target/v1/deploy/checked-apply", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if journal != "" {
			r.Header.Set(journalHeader, journal)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Logf("HTTP%d: %s", w.Code, w.Body.String())
		}
		return w.Code
	}
	if send("monitor", history.journal, prior, id) != 403 {
		t.Fatal("monitor gained checked apply")
	}
	if send("deploy", "", prior, id) != 409 {
		t.Fatal("missing journal admitted")
	}
	if send("deploy", history.journal, strings.Repeat("9", 32), id) != 409 || previews != 0 || calls != 0 {
		t.Fatal("wrong predecessor reached preview/executor")
	}
	if send("deploy", history.journal, prior, id) != 200 || calls != 1 {
		t.Fatal("matching update rejected")
	}
	if send("deploy", history.journal, prior, strings.Repeat("3", 32)) != 409 || calls != 1 {
		t.Fatal("stale preview inherited replacement")
	}
	if send("deploy", history.journal, prior, id) != 409 || calls != 1 {
		t.Fatal("replayed checked request")
	}
}
