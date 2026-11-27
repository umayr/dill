package agentcmd

import (
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"strings"
	"testing"
)

func TestBrokerUnknownOutcomeNeverReplaysAndPersistsReference(t *testing.T) {
	h, ref, _ := brokerFixture(t)
	h.broker.apply = func(_ context.Context, _ string, _ brokersecret.Reference, _ string, id string, _ operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
		return deploymentStatus{RequestID: id, Stack: ref.Scope.Stack, State: "unknown", Effects: "unknown"}, nil
	}
	id := strings.Repeat("3", 32)
	raw, _ := json.Marshal(brokerApplyRequest{Reference: ref, RequestID: id, Confirm: "create", SelectionDigest: operation.Digest("broker selection")})
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-apply": true, "broker-status": true})
	w := brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/apply/"+strings.Repeat("1", 32), "deploy", raw, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"unknown"`) {
		t.Fatal("unknown was not preserved")
	}
	if _, mode := h.history.managed("demo"); mode != "" {
		t.Fatal("unknown allowed retry")
	}
	h.history.dir.Close()
	loaded, err := openDeploymentHistory(h.history.path, h.history.target)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.brokerReference(id) == nil || !loaded.brokerProtected("demo") {
		t.Fatal("unknown lost generation record")
	}
	loaded.dir.Close()
	file, _, err := readOfflineDeploymentJournal(h.history.path)
	if err != nil || file.Version != 3 || len(file.Rows) != 1 || file.Rows[0].Broker == nil {
		t.Fatal("version3 journal unreadable")
	}
	raw, _ = canonicalHistory(file)
	file.Version = 2
	bad, _ := canonicalHistory(file)
	if _, err = parseOfflineDeploymentJournal(bad); err == nil {
		t.Fatal("broker metadata accepted as an older journal version")
	}
	if _, err = parseOfflineDeploymentJournal(raw); err != nil {
		t.Fatal(err)
	}
}
func TestBrokerOrdinaryApplyCannotBypassFailedIntentProtection(t *testing.T) {
	h, ref, _ := brokerFixture(t)
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: strings.Repeat("4", 32), Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: strings.Repeat("1", 32), Manifest: strings.TrimPrefix(ref.Scope.SourceDigest, "sha256:"), Broker: &ref}
	if err := h.history.reserve(row, ""); err != nil {
		t.Fatal(err)
	}
	status := row.deploymentStatus
	status.State = "failed"
	status.Effects = "none"
	if err := h.history.finish(status, nil); err != nil {
		t.Fatal(err)
	}
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"apply": true})
	raw := []byte(`{"stageId":"11111111111111111111111111111111","manifestSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","stack":"demo","selectionDigest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","requestId":"55555555555555555555555555555555","confirm":"create"}`)
	w := brokerRequest(routes, h.history, "POST", "/v1/deploy/apply", "deploy", raw, true)
	if w.Code != 409 {
		t.Fatal("ordinary route bypassed failed-but-protected history")
	}
}
