package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PortBinding is the normalized form of a Pkl Port struct or shorthand string.
// All fields are strings to match container engine APIs.
type PortBinding struct {
	HostIP    string // "" means 0.0.0.0
	Published string // "" means no host port (container-only)
	Target    string
	Protocol  string // "tcp", "udp", or "sctp"
}

// VolumeMount is the normalized form of a Pkl Mount struct or shorthand string.
type VolumeMount struct {
	Type     string // "volume", "bind", "tmpfs"
	Source   string // "" for anonymous volumes
	Target   string
	ReadOnly bool
}

// NormalizePorts converts the raw JSON union slice (string | Port object) into
// concrete PortBinding values. String form: [host_ip:]published:target[/protocol]
func NormalizePorts(raw []json.RawMessage) ([]PortBinding, error) {
	out := make([]PortBinding, 0, len(raw))
	for i, r := range raw {
		pb, err := normalizePort(r)
		if err != nil {
			return nil, fmt.Errorf("port[%d]: %w", i, err)
		}
		out = append(out, pb)
	}
	return out, nil
}

// NormalizeVolumes converts the raw JSON union slice (string | Mount object) into
// concrete VolumeMount values. String form: [source:]target[:ro]
func NormalizeVolumes(raw []json.RawMessage) ([]VolumeMount, error) {
	out := make([]VolumeMount, 0, len(raw))
	for i, r := range raw {
		vm, err := normalizeVolume(r)
		if err != nil {
			return nil, fmt.Errorf("volume[%d]: %w", i, err)
		}
		out = append(out, vm)
	}
	return out, nil
}

// NormalizeHealthCheckTest converts the raw JSON (string | []string) into []string
// suitable for container engine healthcheck test commands.
func NormalizeHealthCheckTest(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Try array first.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	// Fall back to string → CMD-SHELL form.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("healthcheck test must be a string or list of strings")
	}
	return []string{"CMD-SHELL", s}, nil
}

func normalizePort(raw json.RawMessage) (PortBinding, error) {
	// Detect string vs object by first non-whitespace byte.
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return PortBinding{}, err
		}
		return parsePortString(s)
	}

	var p Port
	if err := json.Unmarshal(raw, &p); err != nil {
		return PortBinding{}, fmt.Errorf("invalid Port object: %w", err)
	}
	pb := PortBinding{
		Target:   strconv.Itoa(p.Target),
		Protocol: p.Protocol,
	}
	if p.Published != nil {
		pb.Published = strconv.Itoa(*p.Published)
	}
	if p.HostIP != nil {
		pb.HostIP = *p.HostIP
	}
	return pb, nil
}

// parsePortString handles: target, published:target, host_ip:published:target,
// and optional /protocol suffix on target.
func parsePortString(s string) (PortBinding, error) {
	pb := PortBinding{Protocol: "tcp"}

	// Strip protocol suffix.
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		pb.Protocol = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		pb.Target = parts[0]
	case 2:
		pb.Published = parts[0]
		pb.Target = parts[1]
	case 3:
		pb.HostIP = parts[0]
		pb.Published = parts[1]
		pb.Target = parts[2]
	default:
		return PortBinding{}, fmt.Errorf("invalid port spec %q", s)
	}
	return pb, nil
}

func normalizeVolume(raw json.RawMessage) (VolumeMount, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return VolumeMount{}, err
		}
		return parseVolumeString(s)
	}

	var m Mount
	if err := json.Unmarshal(raw, &m); err != nil {
		return VolumeMount{}, fmt.Errorf("invalid Mount object: %w", err)
	}
	vm := VolumeMount{
		Type:     m.Type,
		Target:   m.Target,
		ReadOnly: m.ReadOnly,
	}
	if m.Source != nil {
		vm.Source = *m.Source
	}
	return vm, nil
}

// parseVolumeString handles: target, source:target, source:target:ro
func parseVolumeString(s string) (VolumeMount, error) {
	vm := VolumeMount{Type: "volume"}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		vm.Target = parts[0]
	case 2:
		vm.Source = parts[0]
		vm.Target = parts[1]
		// Detect bind mount from absolute path.
		if strings.HasPrefix(vm.Source, "/") || strings.HasPrefix(vm.Source, ".") {
			vm.Type = "bind"
		}
	case 3:
		vm.Source = parts[0]
		vm.Target = parts[1]
		if strings.HasPrefix(vm.Source, "/") || strings.HasPrefix(vm.Source, ".") {
			vm.Type = "bind"
		}
		if parts[2] == "ro" {
			vm.ReadOnly = true
		}
	default:
		return VolumeMount{}, fmt.Errorf("invalid volume spec %q", s)
	}
	return vm, nil
}
