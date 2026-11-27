package agentcmd

import (
	"io"
	"strings"
	"testing"
)

func TestBrokerAgentRequiresExplicitOperatorState(t *testing.T) {
	base := []string{"--https-listen", "127.0.0.1:8443", "--https-staging-root", "/operator/stage", "--https-staging-catalog", "/operator/catalog", "--https-deploy-grant", "broker-review"}
	err := serveAgent(base, io.Discard, io.Discard, true)
	if err == nil || !strings.Contains(err.Error(), "broker grants require --https-broker-store") {
		t.Fatal("broker grants silently enabled without state", err)
	}
	err = serveAgent(append(base, "--https-broker-store", "/operator/broker"), io.Discard, io.Discard, true)
	if err == nil || !strings.Contains(err.Error(), "checked apply") {
		t.Fatal("broker store accepted without qualified apply", err)
	}
}
