package agentcmd

import (
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"strings"
	"testing"
)

func TestBrokerRequirementsAreScopedAndValueFree(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	h.broker.requirements = func(_ context.Context, path, stack string) (brokersecret.Requirements, error) {
		*calls++
		if path != "/trusted/main.pkl" || stack != "demo" {
			t.Fatal("untrusted source")
		}
		return brokersecret.Requirements{Contract: brokersecret.Version, Engine: "docker", Stack: stack, ConfigDigest: ref.Scope.ConfigDigest, Names: []string{"password"}, Grants: []brokersecret.Grant{{Service: "web", Name: "password", Target: "/run/secrets/password", Mode: 0400}}}, nil
	}
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-requirements": true})
	raw, _ := json.Marshal(map[string]string{"stack": "demo", "manifestSha256": strings.TrimPrefix(ref.Scope.SourceDigest, "sha256:")})
	w := brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/requirements/"+strings.Repeat("1", 32), "deploy", raw, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), ref.Scope.ConfigDigest) || strings.Contains(w.Body.String(), "/trusted/") {
		t.Fatal("invalid value-free descriptor")
	}
	before := *calls
	w = brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/requirements/"+strings.Repeat("1", 32), "monitor", raw, true)
	if w.Code != 403 || *calls != before {
		t.Fatal("monitor evaluated source")
	}
}
