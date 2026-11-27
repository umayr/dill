package agentcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLifecycleRequiresJournalGrantSelectionAndDoesNotReplay(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			history, e := openDeploymentHistory(historyDirectory(t), operation.Digest("target"))
			if e != nil {
				t.Fatal(e)
			}
			defer history.dir.Close()
			previous, id := strings.Repeat("1", 32), strings.Repeat("2", 32)
			owned := []operation.ManagedResource{{Kind: "container", Name: "demo-web", ID: strings.Repeat("a", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("b", 64)}}
			row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: previous, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: previous, Manifest: strings.Repeat("a", 64)}
			history.reserve(row, "")
			if e = history.finish(deploymentStatus{RequestID: previous, Stack: "demo", State: "succeeded", Effects: "complete", OperationID: "op_" + previous}, owned); e != nil {
				t.Fatal(e)
			}
			selection := "sha256:" + strings.Repeat("c", 64)
			resources := []preflight.Resource{}
			for _, r := range owned {
				resources = append(resources, preflight.Resource{Kind: r.Kind, Name: r.Name, Identity: r.ID, IdentityKind: "immutable-id", Presence: "present"})
			}
			calls, previews := 0, 0
			endpoint := &applyEndpoint{previewEndpoint: previewEndpoint{stages: stageGate(func(_ context.Context, stage, manifest string, f func(string) error) error {
				if stage != row.StageID || manifest != row.Manifest {
					t.Fatal("lifecycle changed retained source")
				}
				return f("/retained/main.pkl")
			})}, history: history, slot: make(chan struct{}, 1), lifecyclePreview: map[string]func(context.Context, string, string) (preflight.Result, error){action: func(context.Context, string, string) (preflight.Result, error) {
				previews++
				return preflight.Result{Operation: action, Target: &preflight.Target{}, SelectionDigest: &selection, Resources: resources}, nil
			}}, lifecycleApply: map[string]func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource){action: func(_ context.Context, _ string, stack, _ string, request, _ string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
				calls++
				if expected.Mode != "update" {
					t.Fatal("lifecycle adopted source")
				}
				return deploymentStatus{RequestID: request, Stack: stack, State: "succeeded", Effects: "none", OperationID: "op_" + request}, owned
			}}}
			handler := authenticatedHTTPS(nil, nil, nil, endpoint, "monitor", "deployer", httpsDeployGrants{action: true, action + "-preview": true})
			request := func(token, journal, body string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("POST", "https://target/v1/deploy/"+action, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("Content-Type", "application/json")
				if journal != "" {
					r.Header.Set(journalHeader, journal)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			body := fmt.Sprintf(`{"stack":"demo","selectionDigest":%q,"requestId":%q,"priorRequestId":%q,"confirm":%q}`, selection, id, previous, action)
			if w := request("monitor", history.journal, body); w.Code != 403 {
				t.Fatal("monitor gained lifecycle", w.Code)
			}
			if w := request("deployer", "", body); w.Code != 409 || calls != 0 {
				t.Fatal("missing journal admitted")
			}
			if w := request("deployer", history.journal, strings.Replace(body, selection, "malformed", 1)); w.Code != 409 || previews != 0 || calls != 0 {
				t.Fatal("malformed digest ran preview", w.Code, previews, calls)
			}
			if w := request("deployer", history.journal, strings.Replace(body, selection, "sha256:"+strings.Repeat("d", 64), 1)); w.Code != 409 || calls != 0 {
				t.Fatal("stale selection response", w.Code, w.Body.String(), calls)
			}
			w := request("deployer", history.journal, body)
			var status deploymentStatus
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Action != action || status.Effects != "none" || status.State != "succeeded" || calls != 1 {
				t.Fatal("lifecycle lost raw no-op result", w.Code, w.Body.String(), calls)
			}
			if w = request("deployer", history.journal, body); w.Code != 409 || calls != 1 {
				t.Fatal("lifecycle replayed")
			}
		})
	}
}
