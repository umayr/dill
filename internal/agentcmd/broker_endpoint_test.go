package agentcmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func brokerFixture(t *testing.T) (*applyEndpoint, brokersecret.Reference, *int) {
	t.Helper()
	history, err := openDeploymentHistory(historyDirectory(t), operation.Digest("history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { history.dir.Close() })
	digest := operation.Digest("snapshot")
	ref := brokersecret.Reference{Handle: strings.Repeat("a", 32), Scope: brokersecret.Scope{Registration: strings.Repeat("b", 32), Stack: "demo", Engine: "docker", TargetDigest: operation.Digest("target"), EndpointDigest: operation.Digest("endpoint"), SourceDigest: digest, ConfigDigest: operation.Digest("config"), PolicyRevision: operation.Digest("policy"), Generation: strings.Repeat("c", 32), BindingRevision: 1}}
	calls := new(int)
	slot := make(chan struct{}, 1)
	b := &brokerEndpoint{history: history, slot: slot, target: ref.Scope.TargetDigest, endpoint: ref.Scope.EndpointDigest}
	b.stages = stageGate(func(_ context.Context, stage, manifest string, f func(string) error) error {
		if stage != strings.Repeat("1", 32) || "sha256:"+manifest != digest {
			t.Fatal("source not independently verified")
		}
		return f("/trusted/main.pkl")
	})
	b.prepare = func(_ context.Context, _ string, e brokersecret.Envelope) (brokersecret.Review, error) {
		*calls++
		return brokersecret.Review{Handle: ref.Handle, Scope: e.Scope, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	selection := operation.Digest("broker selection")
	b.review = func(context.Context, string, brokersecret.Reference) (brokerCLIReply, error) {
		*calls++
		return brokerCLIReply{Contract: brokersecret.Version, Reference: &ref, Preview: &preflight.Result{SelectionDigest: &selection, Resources: []preflight.Resource{{Presence: "absent"}}}}, nil
	}
	b.apply = func(ctx context.Context, _ string, _ brokersecret.Reference, _ string, id string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
		*calls++
		status, ok := history.status(id)
		if !ok || status.State != "running" || ctx.Err() != nil || expected.Mode != "create" {
			t.Fatal("mutation without durable admitted ownership")
		}
		return deploymentStatus{RequestID: id, Stack: "demo", State: "succeeded", Effects: "complete", OperationID: "op_" + id}, []operation.ManagedResource{{Kind: "container", Name: "demo_web", ID: strings.Repeat("d", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("e", 64)}}
	}
	return &applyEndpoint{broker: b, history: history, slot: slot}, ref, calls
}
func brokerRequest(h http.Handler, history *deploymentHistory, method, path, token string, body []byte, headers bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://agent"+path, bytes.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	if headers {
		r.Header.Set(brokerContractHeader, brokersecret.Version)
		r.Header.Set(journalHeader, history.journal)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestBrokerHTTPSExplicitCredentialAndGrants(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	body, _ := json.Marshal(ref)
	path := "/v1/broker-secrets/review/" + strings.Repeat("1", 32)
	for _, tc := range []struct {
		token   string
		grants  httpsDeployGrants
		headers bool
		want    int
	}{
		{"monitor", httpsDeployGrants{"broker-review": true}, true, 403},
		{"deploy", httpsDeployGrants{"preview": true, "apply": true}, true, 403},
		{"deploy", httpsDeployGrants{"broker-review": true}, false, 400},
		{"deploy", httpsDeployGrants{"broker-review": true}, true, 200},
	} {
		before := *calls
		routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", tc.grants)
		w := brokerRequest(routes, h.history, "POST", path, tc.token, body, tc.headers)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.want != 200 && *calls != before {
			t.Fatal("denial reached broker")
		}
	}
}
func TestBrokerHTTPSPreparationAndMetadataRefusals(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-prepare": true, "broker-review": true})
	item, _ := brokersecret.NewItem("password", []byte("synthetic-private-value"))
	e := brokersecret.Envelope{Scope: ref.Scope, Items: []brokersecret.Item{item}}
	defer e.Close()
	raw, _ := brokersecret.Encode(e)
	defer clear(raw)
	w := brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/prepare/"+strings.Repeat("1", 32), "deploy", raw, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-private-value") {
		t.Fatal("preparation response not metadata-only")
	}
	for _, body := range []string{`{"scope":{},"scope":{}}`, `{"file":"/private/key","handle":"abc"}`} {
		before := *calls
		w = brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/review/"+strings.Repeat("1", 32), "deploy", []byte(body), true)
		if w.Code != 400 || *calls != before {
			t.Fatal("bad metadata reached executor")
		}
	}
	foreign := ref
	foreign.Scope.TargetDigest = operation.Digest("foreign")
	raw, _ = json.Marshal(foreign)
	before := *calls
	w = brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/review/"+strings.Repeat("1", 32), "deploy", raw, true)
	if w.Code != 409 || *calls != before {
		t.Fatal("foreign target reached execution")
	}
}
func TestBrokerHTTPSJournalAndStickyProtection(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-apply": true, "broker-status": true, "apply": true, "preview": true})
	id := strings.Repeat("2", 32)
	request := brokerApplyRequest{Reference: ref, RequestID: id, Confirm: "create", SelectionDigest: operation.Digest("broker selection")}
	raw, _ := json.Marshal(request)
	path := "/v1/broker-secrets/apply/" + strings.Repeat("1", 32)
	w := brokerRequest(routes, h.history, "POST", path, "deploy", raw, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"succeeded"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	before := *calls
	w = brokerRequest(routes, h.history, "POST", path, "deploy", raw, true)
	if w.Code != 409 || *calls != before+1 {
		t.Fatal("replayed mutation")
	}
	if !h.history.brokerProtected("demo") {
		t.Fatal("lost sticky protection")
	}
	w = brokerRequest(routes, h.history, "GET", "/v1/broker-secrets/operations/"+id, "deploy", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ref.Handle) {
		t.Fatal("missing exact deployment reference")
	}
	legacy := `{"stack":"demo","stageId":"11111111111111111111111111111111","manifestSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	w = brokerRequest(routes, h.history, "POST", "/v1/deploy/preview", "deploy", []byte(legacy), true)
	if w.Code != 409 {
		t.Fatal("ordinary review bypassed protection")
	}
	historyPath, target := h.history.path, h.history.target
	h.history.dir.Close()
	loaded, err := openDeploymentHistory(historyPath, target)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.dir.Close()
	if !loaded.brokerProtected("demo") || loaded.brokerReference(id) == nil {
		t.Fatal("restart lost protected metadata")
	}
	file, _, err := readOfflineDeploymentJournal(historyPath)
	if err == nil {
		t.Fatal("offline inspector ignored live lock", file)
	}
}
