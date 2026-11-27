package agentcmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrokerReviewRejectsEvenEmptyApplyOnlyFields(t *testing.T) {
	h, ref, calls := brokerFixture(t)
	routes := authenticatedHTTPS(nil, nil, nil, h, "monitor", "deploy", httpsDeployGrants{"broker-review": true})
	raw, _ := json.Marshal(brokerApplyRequest{Reference: ref})
	w := brokerRequest(routes, h.history, "POST", "/v1/broker-secrets/review/"+strings.Repeat("1", 32), "deploy", raw, true)
	if w.Code != 400 || *calls != 0 {
		t.Fatal("review accepted apply-only metadata")
	}
}
