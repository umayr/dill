package orchestrator

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func TestBrokerDockerRawNamespaceEvidence(t *testing.T) {
	for _, tc := range []struct {
		host  string
		valid bool
	}{
		{`{"UsernsMode":""}`, true},
		{`{}`, false},
		{`{"UsernsMode":null}`, false},
		{`{"usernsmode":""}`, false},
		{`{"UsernsMode":"","UsernsMode":"host"}`, false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			calls := 0
			engine := guardrailAdapterRaw(t, "docker", contextTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return contextResponse(r, 200, `{"Id":"immutable","Config":{},"NetworkSettings":{},"HostConfig":`+tc.host+`}`), nil
			})).(*DockerEngine)
			httpClient := engine.cli.HTTPClient()
			original := httpClient.Transport
			guarded, err := brokerDockerInspectClient(httpClient)
			if err != nil || httpClient.Transport != original {
				t.Fatal("changed retained client")
			}
			// The production broker constructor installs this wrapper before SDK use.
			engine.cli, err = client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.43"), client.WithHTTPClient(guarded))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { engine.cli.Close() })
			_, err = engine.InspectConfig(context.Background(), "immutable")
			if (err == nil) != tc.valid || calls != 1 {
				t.Fatalf("wrong raw evidence admission: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestBrokerDockerInspectBoundedAndClosed(t *testing.T) {
	for _, body := range []string{`{"HostConfig":{"UsernsMode":""}}`, strings.Repeat("x", (8<<20)+1)} {
		closed := false
		transport := brokerDockerInspectTransport{contextTransport(func(r *http.Request) (*http.Response, error) {
			response := contextResponse(r, 200, body)
			response.Body = &inspectCloseProbe{ReadCloser: response.Body, closed: &closed}
			return response, nil
		})}
		r, _ := http.NewRequest(http.MethodGet, "http://docker/containers/immutable/json", nil)
		response, err := transport.RoundTrip(r)
		if !closed || (err == nil) != (len(body) <= 8<<20) {
			t.Fatal("unbounded or unclosed inspect")
		}
		if response != nil {
			response.Body.Close()
		}
	}
}

type inspectCloseProbe struct {
	io.ReadCloser
	closed *bool
}

func (p *inspectCloseProbe) Close() error { *p.closed = true; return p.ReadCloser.Close() }
