package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/containers/podman/v5/libpod/define"
	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/umayr/dill/internal/brokersecret"
)

func (p *PodmanEngine) inspectConfigData(ctx context.Context, name string) (*define.InspectContainerData, error) {
	if p.broker || p.qualifiedBroker != nil {
		return p.inspectBrokerPodman(ctx, name)
	}
	return containers.Inspect(p.operationContext(ctx), name, nil)
}

// inspectBrokerPodman uses one qualified request. Inspect's typed SDK decoder
// otherwise erases the distinction between missing UsernsMode and observed "".
func (p *PodmanEngine) inspectBrokerPodman(ctx context.Context, name string) (*define.InspectContainerData, error) {
	ctx = p.operationContext(ctx)
	conn, err := bindings.GetClient(ctx)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	response, err := conn.DoRequest(ctx, nil, http.MethodGet, "/containers/%s/json", nil, nil, name)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		return nil, brokersecret.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil || len(raw) > 8<<20 || !brokerPodmanNamespaceEvidence(raw) {
		return nil, brokersecret.ErrUnavailable
	}
	var data define.InspectContainerData
	if json.Unmarshal(raw, &data) != nil {
		return nil, brokersecret.ErrUnavailable
	}
	return &data, nil
}

// Object-level key checks refuse duplicate/case-alias interpretations before
// the SDK decoder. Unknown ordinary inspect fields remain forward compatible.
func brokerInspectObject(raw []byte) (map[string]json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	result := make(map[string]json.RawMessage)
	seen := make(map[string]bool)
	for d.More() {
		key, err := d.Token()
		s, ok := key.(string)
		if err != nil || !ok || seen[strings.ToLower(s)] {
			return nil, false
		}
		for _, canonical := range []string{"HostConfig", "UsernsMode", "IDMappings", "UidMap", "GidMap"} {
			if strings.EqualFold(s, canonical) && s != canonical {
				return nil, false
			}
		}
		seen[strings.ToLower(s)] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, false
		}
		result[s] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return result, true
}

func brokerPodmanNamespaceEvidence(raw []byte) bool {
	root, ok := brokerInspectObject(raw)
	if !ok {
		return false
	}
	host, ok := brokerInspectObject(root["HostConfig"])
	if !ok {
		return false
	}
	var mode *string
	if json.Unmarshal(host["UsernsMode"], &mode) != nil || mode == nil {
		return false
	}
	// IDMappings is omitted by the pinned producer when no custom maps exist.
	// A present object must contain both exact fields, not SDK-inferred nil maps.
	if maps, exists := host["IDMappings"]; exists && !bytes.Equal(bytes.TrimSpace(maps), []byte("null")) {
		fields, ok := brokerInspectObject(maps)
		if !ok {
			return false
		}
		for _, key := range []string{"UidMap", "GidMap"} {
			var values []string
			v, exists := fields[key]
			if !exists || json.Unmarshal(v, &values) != nil {
				return false
			}
		}
	}
	return true
}
