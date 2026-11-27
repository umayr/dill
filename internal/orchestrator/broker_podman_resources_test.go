package orchestrator

import (
	"context"
	"net/http"
	"testing"
)

func TestBrokerPodmanResourceRawPresence(t *testing.T) {
	positive := `{"name":"synthetic","id":"id","driver":"bridge","network_interface":"podman1","subnets":[],"ipv6_enabled":false,"internal":false,"dns_enabled":false,"labels":{},"ipam_options":{}}`
	if !brokerPodmanResourceEvidence("networks", []byte(positive)) {
		t.Fatal("complete raw network refused")
	}
	for _, raw := range []string{`{}`, `{"internal":false}`, `{"internal":null}`, `{"internal":false,"internal":true}`} {
		if brokerPodmanResourceEvidence("networks", []byte(raw)) {
			t.Fatal("missing/duplicate namespace facts accepted")
		}
	}
	calls := 0
	wrapped := brokerPodmanResourceTransport{contextTransport(func(r *http.Request) (*http.Response, error) { calls++; return contextResponse(r, 200, "[]"), nil })}
	r, _ := http.NewRequestWithContext(context.Background(), "GET", "http://d/v5.0.0/libpod/networks/json", nil)
	response, err := wrapped.RoundTrip(r)
	if err != nil || calls != 1 {
		t.Fatal("list was treated as inspect", err)
	}
	response.Body.Close()
	r, _ = http.NewRequestWithContext(context.Background(), "GET", "http://d/v5.0.0/libpod/networks/synthetic/json", nil)
	if _, err = wrapped.RoundTrip(r); err == nil || calls != 2 {
		t.Fatal("unknown raw inspect admitted or retried")
	}
}
