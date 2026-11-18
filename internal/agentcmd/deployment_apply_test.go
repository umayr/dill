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
	if err := h.reserve(id, "demo"); err != nil {
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
	if h.canReserve(id, "different") || h.canReserve(strings.Repeat("2", 32), "demo") {
		t.Fatal("restart admitted replay")
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
		apply: func(ctx context.Context, path, stack, digest, request, _ string) deploymentStatus {
			calls++
			got, ok := history.status(id)
			if !ok || got.State != "running" || ctx.Err() != nil || path != "/stages/sample/main.pkl" || request != id || digest != selection {
				t.Fatal("launch did not bind durable verified intent")
			}
			return deploymentStatus{RequestID: id, Stack: stack, State: "succeeded", Effects: "complete", OperationID: "op_" + id}
		},
		slot: make(chan struct{}, 1),
	}
	routes := authenticatedHTTPS(nil, nil, nil, h, "secret-token")
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
