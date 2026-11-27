package orchestrator

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/umayr/dill/internal/brokersecret"
)

func brokerPodmanResourceClient(client *http.Client) (*http.Client, error) {
	if client == nil || client.Transport == nil {
		return nil, brokersecret.ErrUnavailable
	}
	copy := *client
	copy.Transport = brokerPodmanResourceTransport{client.Transport}
	return &copy, nil
}

type brokerPodmanResourceTransport struct{ base http.RoundTripper }

func (t brokerPodmanResourceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil || response == nil {
		return response, err
	}
	parts := strings.Split(r.URL.Path, "/")
	n := len(parts)
	kind := ""
	if r.Method == http.MethodGet && n >= 4 && parts[n-1] == "json" && parts[n-2] != "" && (parts[n-3] == "networks" || parts[n-3] == "volumes") {
		kind = parts[n-3]
	}
	if kind == "" || response.StatusCode != 200 {
		return response, nil
	}
	if response.Body == nil {
		return nil, brokersecret.ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	ce := response.Body.Close()
	if e != nil || ce != nil || len(raw) > 1<<20 || !brokerPodmanResourceEvidence(kind, raw) {
		return nil, brokersecret.ErrUnavailable
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}
func brokerPodmanResourceEvidence(kind string, raw []byte) bool {
	fields, ok := brokerInspectObject(raw)
	if !ok {
		return false
	}
	required := []string{"Name", "Driver", "Mountpoint", "CreatedAt", "Options", "Labels"}
	if kind == "networks" {
		required = []string{"name", "id", "driver", "network_interface", "subnets", "ipv6_enabled", "internal", "dns_enabled", "labels", "ipam_options"}
	} else if kind != "volumes" {
		return false
	}
	for _, key := range required {
		value, exists := fields[key]
		if !exists {
			return false
		}
		if kind == "networks" && (key == "ipv6_enabled" || key == "internal" || key == "dns_enabled") {
			var flag *bool
			if json.Unmarshal(value, &flag) != nil || flag == nil {
				return false
			}
		}
	}
	return true
}
