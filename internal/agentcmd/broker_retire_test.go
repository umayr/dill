package agentcmd

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"strings"
	"testing"
	"time"
)

func TestBrokerRetirementSeparateGrantAndManagedPredecessor(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	id := strings.Repeat("7", 32)
	next := ref.Scope
	next.Generation = strings.Repeat("d", 32)
	next.BindingRevision++
	req := brokersecret.Retirement{Contract: brokersecret.RetirementVersion, Scope: next, Previous: []brokersecret.Reference{ref}}
	raw, err := brokersecret.EncodeRetirement(req)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/broker-secrets/retire/" + strings.Repeat("1", 32)
	h.broker.retire = func(_ context.Context, path string, r brokersecret.Retirement) (brokersecret.Review, error) {
		*calls++
		if path != "/trusted/main.pkl" || r.Scope != next {
			t.Fatal("unverified source/scope")
		}
		return brokersecret.Review{Handle: strings.Repeat("9", 32), Scope: r.Scope, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	for _, grants := range []httpsDeployGrants{{"broker-prepare": true}, {"broker-review": true, "broker-apply": true}} {
		routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", grants)
		w := brokerRequest(routes, h.history, "POST", path, "deploy", raw, true)
		if w.Code != 403 || *calls != 0 {
			t.Fatal("ordinary broker grants inherited retirement")
		}
	}
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-retire": true})
	if w := brokerRequest(routes, h.history, "POST", path, "monitor", raw, true); w.Code != 403 {
		t.Fatal("monitor retirement authority")
	}
	if w := brokerRequest(routes, h.history, "POST", path, "deploy", raw, true); w.Code != 409 || *calls != 0 {
		t.Fatal("unmanaged predecessor accepted")
	}
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "pending", Effects: "unknown"}, Mode: "create", StageID: strings.Repeat("1", 32), Manifest: strings.TrimPrefix(ref.Scope.SourceDigest, "sha256:"), Broker: &ref}
	if err = h.history.reserve(row, ""); err != nil {
		t.Fatal(err)
	}
	owned := []operation.ManagedResource{{Kind: "container", Name: "demo_web", ID: strings.Repeat("4", 64)}, {Kind: "network", Name: "demo", ID: strings.Repeat("5", 64)}}
	if err = h.history.finish(deploymentStatus{RequestID: id, Stack: "demo", State: "succeeded", Effects: "complete", OperationID: "op_" + id}, owned); err != nil {
		t.Fatal(err)
	}
	w := brokerRequest(routes, h.history, "POST", path, "deploy", raw, true)
	if w.Code != 200 || *calls != 1 {
		t.Fatal("exact retirement refused", w.Code, w.Body.String())
	}
	bad := req
	bad.Previous = append([]brokersecret.Reference(nil), req.Previous...)
	bad.Previous[0].Handle = strings.Repeat("6", 32)
	other, _ := brokersecret.EncodeRetirement(bad)
	w = brokerRequest(routes, h.history, "POST", path, "deploy", other, true)
	if w.Code != 409 || *calls != 1 {
		t.Fatal("foreign managed predecessor reached child")
	}
	if h.history.brokerProtected("demo") != true {
		t.Fatal("retirement cleared protection")
	}
}
