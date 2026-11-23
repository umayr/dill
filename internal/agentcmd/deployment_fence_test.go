package agentcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func TestFenceWinsWhileApplyIsInPreflight(t *testing.T) {
	path, target := historyDirectory(t), operation.Digest("native target")
	history, err := openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	journal := history.journal
	stage, id, manifest := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 64)
	selection := "sha256:" + strings.Repeat("4", 64)
	started, unblock := make(chan struct{}), make(chan struct{})
	h := &applyEndpoint{history: history, slot: make(chan struct{}, 1), previewEndpoint: previewEndpoint{
		stages: stageGate(func(_ context.Context, _, _ string, fn func(string) error) error {
			return fn("/stages/demo/compose.pkl")
		}),
		preview: func(ctx context.Context, _, _ string) (preflight.Result, error) {
			close(started)
			select {
			case <-unblock:
			case <-ctx.Done():
				return preflight.Result{}, ctx.Err()
			}
			return preflight.Result{Target: &preflight.Target{}, SelectionDigest: &selection, Resources: []preflight.Resource{{Presence: "absent"}}}, nil
		},
	}, apply: func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
		t.Error("fenced apply reached executor")
		return deploymentStatus{}, nil
	}}
	handler := authenticatedHTTPS(nil, nil, nil, h, "synthetic-monitor", "credential", httpsDeployGrants{"fence": true, "status": true, "journal-apply": true, "apply": true})
	requestWith := func(routes http.Handler, method, path, token, scope, doc string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://agent"+path, strings.NewReader(doc))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if scope != "" {
			r.Header.Set(journalHeader, scope)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	request := func(method, path, token, scope, doc string) *httptest.ResponseRecorder {
		return requestWith(handler, method, path, token, scope, doc)
	}
	fenceDoc := fmt.Sprintf(`{"requestId":%q,"stack":"demo","confirm":"fence-if-unadmitted"}`, id)
	fence := func(scope string) *httptest.ResponseRecorder {
		return request("POST", "/v1/deploy/fence", "credential", scope, fenceDoc)
	}
	if w := request("POST", "/v1/deploy/fence", "wrong", journal, fenceDoc); w.Code != http.StatusUnauthorized || w.Header().Get(journalHeader) != "" {
		t.Fatalf("fence with invalid token: %d", w.Code)
	}
	if w := request("POST", "/v1/deploy/fence", "synthetic-monitor", journal, fenceDoc); w.Code != http.StatusForbidden || w.Header().Get(journalHeader) != "" {
		t.Fatalf("fence with monitor token: %d", w.Code)
	}
	statusOnly := authenticatedHTTPS(nil, nil, nil, h, "synthetic-monitor", "credential", httpsDeployGrants{"status": true})
	if w := requestWith(statusOnly, "POST", "/v1/deploy/fence", "credential", journal, fenceDoc); w.Code != http.StatusForbidden || w.Header().Get(journalHeader) != "" {
		t.Fatalf("status grant authorized fence: %d", w.Code)
	}
	for _, scope := range []string{"", strings.Repeat("5", 32)} {
		if w := fence(scope); w.Code != 409 {
			t.Fatalf("wrong journal fence: %d", w.Code)
		}
	}
	statusPath := "/v1/deploy/operations/" + id
	if w := request("GET", statusPath, "credential", journal, ""); w.Code != 404 {
		t.Fatal("missing lookup not reported as missing")
	}
	applyDoc := fmt.Sprintf(`{"stageId":%q,"manifestSha256":%q,"stack":"demo","selectionDigest":%q,"requestId":%q,"confirm":"create"}`, stage, manifest, selection, id)
	done := make(chan *httptest.ResponseRecorder, 1)
	for _, scope := range []string{"", strings.Repeat("5", 32)} {
		if w := request("POST", "/v1/deploy/journal-apply", "credential", scope, applyDoc); w.Code != 409 {
			t.Fatalf("unbound apply: %d", w.Code)
		}
	}
	go func() { done <- request("POST", "/v1/deploy/journal-apply", "credential", journal, applyDoc) }()
	select {
	case <-started:
	case early := <-done:
		t.Fatalf("journal-bound apply never entered preflight: %d", early.Code)
	}
	w := fence(journal)
	close(unblock)
	if w.Code != 200 || w.Header().Get(journalHeader) != journal || !strings.Contains(w.Body.String(), `"state":"not-admitted"`) {
		t.Fatalf("fence unavailable: %d %s", w.Code, w.Body.String())
	}
	if applied := <-done; applied.Code != 409 {
		t.Fatalf("late admission was not refused: %d", applied.Code)
	}
	if err := history.dir.Close(); err != nil {
		t.Fatal(err)
	}
	history, err = openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	defer history.dir.Close()
	h.history = history
	if history.journal != journal {
		t.Fatal("journal changed on restart")
	}
	if w := request("GET", statusPath, "credential", journal, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"not-admitted"`) {
		t.Fatal("fence lost across restart")
	}
	if w := request("POST", "/v1/deploy/apply", "credential", journal, applyDoc); w.Code != 409 {
		t.Fatal("fenced request reused")
	}
	if _, mode := history.managed("demo"); mode != "create" {
		t.Fatal("fence altered stack ownership")
	}
}

func TestFencePreservesAdmittedOperationAndLegacyJournal(t *testing.T) {
	path, target := historyDirectory(t), operation.Digest("native target")
	id := strings.Repeat("f", 32)
	existing := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: strings.Repeat("a", 32), Manifest: strings.Repeat("b", 64)}
	raw, err := json.Marshal(deploymentHistoryFile{Version: 1, Target: target, Rows: []deploymentRecord{existing}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "operations.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	if !validApplyID(h.journal) {
		t.Fatal("legacy journal not initialized")
	}
	if status, err := h.fence(id, "demo"); err != nil || status.State != "unknown" {
		t.Fatalf("admitted crash outcome overwritten: %v %v", status, err)
	}
	if _, err := h.fence(id, "another"); err == nil {
		t.Fatal("fence accepted mismatched stack")
	}
	next := existing
	next.RequestID = strings.Repeat("e", 32)
	next.Stack = "another"
	if err := h.reserve(next, ""); err != nil {
		t.Fatal(err)
	}
	if status, err := h.fence(next.RequestID, next.Stack); err != nil || status.State != "running" {
		t.Fatalf("running operation overwritten: %v %v", status, err)
	}
	other, err := openDeploymentHistory(historyDirectory(t), target)
	if err != nil {
		t.Fatal(err)
	}
	defer other.dir.Close()
	if other.journal == h.journal {
		t.Fatal("a replacement journal reused the same identity")
	}
}
