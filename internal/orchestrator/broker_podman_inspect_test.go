package orchestrator

import (
	"context"
	"net/http"
	"testing"
)

func TestBrokerPodmanMissingNamespaceEvidence(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"HostConfig":{"UsernsMode":""}}`, true},
		{`{"HostConfig":{"UsernsMode":"","IDMappings":{"UidMap":[],"GidMap":[]}}}`, true},
		{`{"HostConfig":{}}`, false},
		{`{"HostConfig":{"UsernsMode":null}}`, false},
		{`{"HostConfig":{"UsernsMode":"","UsernsMode":"private"}}`, false},
		{`{"HostConfig":{"usernsmode":""}}`, false},
		{`{"HostConfig":{"UsernsMode":"","idMappings":{}}}`, false},
		{`{"HostConfig":{"UsernsMode":"","IDMappings":{}}}`, false},
		{`{"HostConfig":{"UsernsMode":"","IDMappings":{"UidMap":[]}}}`, false},
		{`{"HostConfig":{"UsernsMode":""}} {}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			if brokerPodmanNamespaceEvidence([]byte(tc.body)) != tc.valid {
				t.Fatal("wrong raw evidence admission")
			}
			calls := 0
			engine := guardrailAdapterRaw(t, "podman", contextTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return contextResponse(r, 200, tc.body), nil
			}))
			p := engine.(*PodmanEngine)
			p.broker = true
			_, err := p.InspectConfig(context.Background(), "immutable")
			if (err == nil) != tc.valid || calls != 1 {
				t.Fatalf("adapter did not enforce one raw inspect: calls=%d err=%v", calls, err)
			}
		})
	}
}
