package orchestrator

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/umayr/dill/internal/brokersecret"
)

// Wrap only the qualified broker SDK client, retaining its exact transport,
// redirect policy and timeout. Never mutate or replace the retained connection.
func brokerDockerInspectClient(client *http.Client) (*http.Client, error) {
	if client == nil || client.Transport == nil {
		return nil, brokersecret.ErrUnavailable
	}
	copy := *client
	copy.Transport = brokerDockerInspectTransport{client.Transport}
	return &copy, nil
}

// A container list ends in /containers/json, not /containers/{id}/json.
func brokerDockerContainerInspectPath(path string) bool {
	parts := strings.Split(path, "/")
	n := len(parts)
	return n >= 4 && parts[n-3] == "containers" && parts[n-2] != "" && parts[n-1] == "json"
}

type brokerDockerInspectTransport struct{ base http.RoundTripper }

func (t brokerDockerInspectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil || response == nil {
		return response, err
	}
	if r.Method != http.MethodGet || !brokerDockerContainerInspectPath(r.URL.Path) || response.StatusCode != http.StatusOK {
		return response, nil
	}
	// Preserve the single response, but prove the raw namespace fact before the
	// Docker SDK can turn a missing field into an empty default string.
	if response.Body == nil {
		return nil, brokersecret.ErrUnavailable
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(raw) > 8<<20 || !brokerPodmanNamespaceEvidence(raw) {
		return nil, brokersecret.ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}
