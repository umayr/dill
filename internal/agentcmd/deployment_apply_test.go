package agentcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func historyDirectory(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "history")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeploymentHistoryRestartNeverReplays(t *testing.T) {
	path := historyDirectory(t)
	scope := operation.Digest("test engine")
	h, err := openDeploymentHistory(path, scope)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("1", 32)
	if _, err := openDeploymentHistory(path, scope); err == nil {
		t.Fatal("opened a second writer")
	}
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: id, Manifest: strings.Repeat("a", 64)}
	if err := h.reserve(row, ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := h.status(id); !ok || got.State != "running" {
		t.Fatal("missing active intent")
	}
	if err := h.dir.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := openDeploymentHistory(path, operation.Digest("different engine")); err == nil {
		t.Fatal("reopened history for a different engine")
	}
	h, err = openDeploymentHistory(path, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	if got, ok := h.status(id); !ok || got.State != "unknown" || got.Effects != "unknown" {
		t.Fatal("crash did not remain unknown")
	}
	replay := row
	replay.Stack = "different"
	if h.reserve(replay, "") == nil {
		t.Fatal("restart admitted duplicate request")
	}
	replay = row
	replay.RequestID = strings.Repeat("2", 32)
	if h.reserve(replay, "") == nil {
		t.Fatal("restart admitted write for blocked stack")
	}
}

func TestDeploymentHistoryManagedTransitions(t *testing.T) {
	p, scope := historyDirectory(t), operation.Digest("bound target")
	h, err := openDeploymentHistory(p, scope)
	if err != nil {
		t.Fatal(err)
	}
	owned := []operation.ManagedResource{{Kind: "container", Name: "demo-app", ID: strings.Repeat("a", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("b", 64)}}
	row := func(ch, mode string) deploymentRecord {
		return deploymentRecord{deploymentStatus: deploymentStatus{RequestID: strings.Repeat(ch, 32), Stack: "demo", State: "pending", Effects: "unknown"}, Mode: mode, StageID: strings.Repeat(ch, 32), Manifest: strings.Repeat(ch, 64)}
	}
	create := row("1", "create")
	if err := h.reserve(create, ""); err != nil {
		t.Fatal(err)
	}
	finished := create.deploymentStatus
	finished.State, finished.Effects, finished.OperationID = "succeeded", "complete", "op_"+create.RequestID
	if err := h.finish(finished, owned); err != nil {
		t.Fatal(err)
	}
	if err := h.dir.Close(); err != nil {
		t.Fatal(err)
	}
	h, err = openDeploymentHistory(p, scope)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	got, mode := h.managed("demo")
	if mode != "update" || got.StageID != create.StageID || len(got.Owned) != 2 || got.Owned[1] != owned[1] {
		t.Fatal("lost persisted ownership")
	}
	update := row("2", "update")
	if h.reserve(update, "") == nil {
		t.Fatal("unbound predecessor admitted")
	}
	if err := h.reserve(update, create.RequestID); err != nil {
		t.Fatal(err)
	}
	finished = update.deploymentStatus
	finished.State, finished.Effects = "failed", "none"
	if err := h.finish(finished, nil); err != nil {
		t.Fatal(err)
	}
	remove := row("3", "remove")
	if err := h.reserve(remove, create.RequestID); err != nil {
		t.Fatal(err)
	}
	finished = remove.deploymentStatus
	finished.State, finished.Effects, finished.OperationID = "succeeded", "complete", "op_"+remove.RequestID
	if err := h.finish(finished, nil); err != nil {
		t.Fatal(err)
	}
	if _, mode := h.managed("demo"); mode != "" {
		t.Fatal("tombstoned project can be adopted")
	}
	if h.reserve(row("4", "create"), "") == nil {
		t.Fatal("tombstoned stack recreated")
	}
}

func TestHTTPSApplyRevalidatesAndDoesNotAdopt(t *testing.T) {
	history, err := openDeploymentHistory(historyDirectory(t), operation.Digest("test engine"))
	if err != nil {
		t.Fatal(err)
	}
	defer history.dir.Close()
	id, stage, manifest := strings.Repeat("8", 32), strings.Repeat("1", 32), strings.Repeat("4", 64)
	selection := "sha256:" + strings.Repeat("a", 64)
	calls, validations := 0, 0
	presence := "present"
	h := &applyEndpoint{
		previewEndpoint: previewEndpoint{
			stages: stageGate(func(_ context.Context, gotStage, gotDigest string, apply func(string) error) error {
				validations++
				if gotStage != stage || gotDigest != manifest {
					t.Fatal("unbound stage")
				}
				return apply("/stages/sample/main.pkl")
			}),
			preview: func(context.Context, string, string) (preflight.Result, error) {
				return preflight.Result{Target: &preflight.Target{}, SelectionDigest: &selection, Resources: []preflight.Resource{{Presence: presence}}}, nil
			},
		},
		history: history,
		apply: func(ctx context.Context, path, stack, digest, request, _ string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
			calls++
			got, ok := history.status(id)
			if !ok || got.State != "running" || ctx.Err() != nil || path != "/stages/sample/main.pkl" || request != id || digest != selection {
				t.Fatal("launch did not bind durable verified intent")
			}
			if expected.Mode != "create" {
				t.Fatal("wrong expected mode")
			}
			return deploymentStatus{RequestID: id, Stack: stack, State: "succeeded", Effects: "complete", OperationID: "op_" + id}, []operation.ManagedResource{
				{Kind: "container", Name: "demo-app", ID: strings.Repeat("1", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("2", 64)},
			}
		},
		slot: make(chan struct{}, 1),
	}
	routes := authenticatedHTTPS(nil, nil, nil, h, "synthetic-monitor", "secret-token", httpsDeployGrants{"apply": true, "status": true})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://agent"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	doc := fmt.Sprintf(`{"stageId":%q,"manifestSha256":%q,"stack":"demo","selectionDigest":%q,"requestId":%q,"confirm":"create"}`, stage, manifest, selection, id)
	if w := request("POST", "/v1/deploy/apply", "bad", doc); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/v1/deploy/apply", "secret-token", doc); w.Code != 409 || calls != 0 {
		t.Fatal("adopted existing resources", w.Code)
	}
	presence = "absent"
	stale := strings.Replace(doc, selection, "sha256:"+strings.Repeat("b", 64), 1)
	if w := request("POST", "/v1/deploy/apply", "secret-token", stale); w.Code != 409 || calls != 0 {
		t.Fatal("accepted stale preview", w.Code)
	}
	before := validations
	if w := request("POST", "/v1/deploy/apply", "secret-token", doc); w.Code != 200 || calls != 1 || validations != before+2 {
		t.Fatal("checked creation failed", w.Code)
	}
	if w := request("POST", "/v1/deploy/apply", "secret-token", doc); w.Code != 409 || calls != 1 {
		t.Fatal("replayed creation", w.Code)
	}
	var status deploymentStatus
	w := request("GET", "/v1/deploy/operations/"+id, "secret-token", "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.State != "succeeded" || status.OperationID != "op_"+id {
		t.Fatal("durable status missing", w.Code)
	}
	if w := request("GET", "/v1/deploy/operations/"+id, "bad", ""); w.Code != 401 {
		t.Fatal("operation status leaked", w.Code)
	}
}
