package config

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
// baseDir is the directory of the config file; relative bind-mount sources are
// resolved against it so that engines always receive absolute host paths.
func NormalizeVolumes(raw []json.RawMessage, baseDir string) ([]VolumeMount, error) {
	out := make([]VolumeMount, 0, len(raw))
	for i, r := range raw {
		vm, err := normalizeVolume(r, baseDir)
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

// pklDuration is the JSON representation Pkl uses for Duration values.
type pklDuration struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

// ParsePklDuration converts a Pkl-serialised Duration ({"value": 30, "unit": "s"})
// into a Go time.Duration. Returns 0 for a nil pointer.
func ParsePklDuration(raw *json.RawMessage) (time.Duration, error) {
	if raw == nil {
		return 0, nil
	}
	var d pklDuration
	if err := json.Unmarshal(*raw, &d); err != nil {
		return 0, fmt.Errorf("parsing pkl duration: %w", err)
	}
	var unit time.Duration
	switch d.Unit {
	case "ns":
		unit = time.Nanosecond
	case "us":
		unit = time.Microsecond
	case "ms":
		unit = time.Millisecond
	case "s":
		unit = time.Second
	case "min":
		unit = time.Minute
	case "h":
		unit = time.Hour
	case "d":
		unit = 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown pkl duration unit %q", d.Unit)
	}
	return time.Duration(d.Value * float64(unit)), nil
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
	if pb.Protocol == "" {
		pb.Protocol = "tcp"
	}
	if pb.Protocol != "tcp" && pb.Protocol != "udp" && pb.Protocol != "sctp" {
		return PortBinding{}, fmt.Errorf("invalid protocol %q", pb.Protocol)
	}
	if err := validatePort(pb.Target, "target"); err != nil {
		return PortBinding{}, err
	}
	if p.Published != nil {
		pb.Published = strconv.Itoa(*p.Published)
		if err := validatePort(pb.Published, "published"); err != nil {
			return PortBinding{}, err
		}
	}
	if p.HostIP != nil {
		pb.HostIP = *p.HostIP
		if pb.HostIP != "" && net.ParseIP(pb.HostIP) == nil {
			return PortBinding{}, fmt.Errorf("invalid host IP %q", pb.HostIP)
		}
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
	if pb.Protocol != "tcp" && pb.Protocol != "udp" && pb.Protocol != "sctp" {
		return PortBinding{}, fmt.Errorf("invalid protocol %q", pb.Protocol)
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
	if err := validatePort(pb.Target, "target"); err != nil {
		return PortBinding{}, err
	}
	if pb.Published != "" {
		if err := validatePort(pb.Published, "published"); err != nil {
			return PortBinding{}, err
		}
	}
	if pb.HostIP != "" && net.ParseIP(pb.HostIP) == nil {
		return PortBinding{}, fmt.Errorf("invalid host IP %q", pb.HostIP)
	}
	return pb, nil
}

func validatePort(raw, field string) error {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("invalid %s port %q", field, raw)
	}
	if n < 0 || n > 65535 {
		return fmt.Errorf("invalid %s port %q: must be between 0 and 65535", field, raw)
	}
	if field == "target" && n == 0 {
		return fmt.Errorf("invalid target port %q: must be between 1 and 65535", raw)
	}
	return nil
}

func normalizeVolume(raw json.RawMessage, baseDir string) (VolumeMount, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return VolumeMount{}, err
		}
		return parseVolumeString(s, baseDir)
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
		vm.Source = resolveBindSource(*m.Source, baseDir)
	}
	return vm, nil
}

// parseVolumeString handles: target, source:target, source:target:ro
func parseVolumeString(s, baseDir string) (VolumeMount, error) {
	vm := VolumeMount{Type: "volume"}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		vm.Target = parts[0]
	case 2:
		vm.Source = parts[0]
		vm.Target = parts[1]
		if strings.HasPrefix(vm.Source, "/") || strings.HasPrefix(vm.Source, ".") {
			vm.Type = "bind"
			vm.Source = resolveBindSource(vm.Source, baseDir)
		}
	case 3:
		vm.Source = parts[0]
		vm.Target = parts[1]
		if strings.HasPrefix(vm.Source, "/") || strings.HasPrefix(vm.Source, ".") {
			vm.Type = "bind"
			vm.Source = resolveBindSource(vm.Source, baseDir)
		}
		if parts[2] == "ro" {
			vm.ReadOnly = true
		} else if parts[2] != "" {
			return VolumeMount{}, fmt.Errorf("invalid volume option %q", parts[2])
		}
	default:
		return VolumeMount{}, fmt.Errorf("invalid volume spec %q", s)
	}
	return vm, nil
}

// resolveBindSource converts a potentially relative bind-mount host path to an
// absolute path anchored at baseDir (the config file's directory).
func resolveBindSource(source, baseDir string) string {
	if filepath.IsAbs(source) {
		return source
	}
	return filepath.Join(baseDir, source)
}
