package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/umayr/dill/internal/config"
)

// Capabilities describes adapter-level support, not host permissions or runtime
// availability. It is an in-process contract, not a versioned operation protocol.
type Capabilities struct {
	Engine           string   `json:"engine"`
	Devices          bool     `json:"devices"`
	UnpublishedPorts bool     `json:"unpublishedPorts"`
	NetworkModes     []string `json:"networkModes"` // empty means engine-native modes
}

// DiscoverCapabilities returns a fresh snapshot of the adapter's known limits.
func DiscoverCapabilities(engine string) (Capabilities, error) {
	switch engine {
	case "docker":
		return Capabilities{Engine: "docker", Devices: true, UnpublishedPorts: true}, nil
	case "", "podman":
		return Capabilities{Engine: "podman", NetworkModes: []string{"host", "none"}}, nil
	default:
		return Capabilities{}, fmt.Errorf("unknown engine %q", engine)
	}
}

// ValidateCapabilities rejects unsupported intent before any deployment mutation.
// It does not replace configuration validation or target-side checks.
func ValidateCapabilities(engine string, services map[string]*config.Service) error {
	caps, err := DiscoverCapabilities(engine)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := services[name]
		if svc == nil {
			return fmt.Errorf("service %q: nil service", name)
		}
		if !caps.Devices && len(svc.Devices) > 0 {
			return fmt.Errorf("service %q: %s adapter does not support device mappings", name, caps.Engine)
		}
		if caps.Engine == "podman" && svc.NetworkMode != "" && svc.NetworkMode != "host" && svc.NetworkMode != "none" {
			return fmt.Errorf("service %q: podman adapter does not support network mode %q (supported: host, none)", name, svc.NetworkMode)
		}
		if !caps.UnpublishedPorts {
			ports, err := config.NormalizePorts(svc.Ports)
			if err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			for _, p := range ports {
				if strings.TrimSpace(p.Published) == "" {
					return fmt.Errorf("service %q: podman adapter does not support unpublished port %s/%s", name, p.Target, p.Protocol)
				}
			}
		}
	}
	return nil
}
