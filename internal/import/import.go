// Package importer translates a deliberately narrow Compose subset to Pkl.
// It does not evaluate configuration, inspect an engine or create files.
package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/umayr/dill/internal/config"
	"gopkg.in/yaml.v3"
)

const MaxInputBytes = 1 << 20

var serviceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
var envName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type service struct {
	image  string
	env    map[string]string
	ports  []string
	mounts []mount
}
type mount struct {
	source, target string
	readOnly       bool
}

// Convert reads a single Compose YAML document and generates a Pkl module.
// Unsupported or ambiguous features fail rather than being silently omitted.
// schema is the explicitly selected Pkl amends URI/path. No imports are fetched.
func Convert(r io.Reader, schema string) ([]byte, error) {
	if schema == "" || !utf8.ValidString(schema) || strings.ContainsAny(schema, "\r\n\x00") {
		return nil, fmt.Errorf("invalid schema path")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read compose: input read failed")
	}
	if len(data) > MaxInputBytes {
		return nil, fmt.Errorf("compose input exceeds size limit")
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("compose input is not UTF-8")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid compose YAML")
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("compose must contain exactly one YAML document")
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("compose document must be a mapping")
	}
	if err := checkNode(doc.Content[0], 0, new(int)); err != nil {
		return nil, err
	}
	root, err := mapping(doc.Content[0], "compose", "services", "version")
	if err != nil {
		return nil, err
	}
	if v := root["version"]; v != nil && (v.Kind != yaml.ScalarNode || v.Tag != "!!str") {
		return nil, problem(v, "version must be a string")
	}
	svcs := root["services"]
	if svcs == nil || svcs.Kind != yaml.MappingNode || len(svcs.Content) == 0 {
		return nil, problem(svcs, "services must be a non-empty mapping")
	}
	services := make(map[string]service)
	for i := 0; i < len(svcs.Content); i += 2 {
		key, node := svcs.Content[i], svcs.Content[i+1]
		if key.Tag != "!!str" || !serviceName.MatchString(key.Value) {
			return nil, problem(key, "invalid service name")
		}
		if _, exists := services[key.Value]; exists {
			return nil, problem(key, "duplicate service")
		}
		s, err := parseService(node)
		if err != nil {
			return nil, err
		}
		services[key.Value] = s
	}
	if err := validateConverted(services); err != nil {
		return nil, fmt.Errorf("compose: translated configuration failed Dill runtime validation")
	}
	return render(schema, services), nil
}

// Validate the translated values using the same normalizers as Dill. This does
// not evaluate the output schema or prove equivalence with Compose behavior.
func validateConverted(services map[string]service) error {
	cfg := &config.DillConfig{Services: make(map[string]*config.Service, len(services))}
	for name, s := range services {
		svc := &config.Service{Image: s.image, Environment: s.env}
		for _, p := range s.ports {
			raw, err := json.Marshal(p)
			if err != nil {
				return err
			}
			svc.Ports = append(svc.Ports, raw)
		}
		for _, m := range s.mounts {
			source := m.source
			raw, err := json.Marshal(config.Mount{Type: "bind", Source: &source, Target: m.target, ReadOnly: m.readOnly})
			if err != nil {
				return err
			}
			svc.Volumes = append(svc.Volumes, raw)
		}
		cfg.Services[name] = svc
	}
	return config.ValidateRuntime(cfg)
}

func problem(n *yaml.Node, message string) error {
	if n == nil {
		return fmt.Errorf("compose: %s", message)
	}
	return fmt.Errorf("compose line %d: %s", n.Line, message)
}

// Reject aliases, merges, duplicate keys and deep/large node trees before parsing.
func checkNode(n *yaml.Node, depth int, count *int) error {
	*count++
	if *count > 20000 || depth > 32 {
		return problem(n, "YAML structure exceeds limits")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return problem(n, "YAML aliases and anchors are unsupported")
	}
	if n.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Value == "<<" {
				return problem(k, "mapping keys must be strings; merges are unsupported")
			}
			if seen[k.Value] {
				return problem(k, "duplicate mapping key")
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if err := checkNode(c, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func mapping(n *yaml.Node, label string, allowed ...string) (map[string]*yaml.Node, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, problem(n, label+" must be a mapping")
	}
	accept := map[string]bool{}
	for _, k := range allowed {
		accept[k] = true
	}
	out := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if !accept[k.Value] {
			return nil, problem(k, "unsupported "+label+" field")
		}
		out[k.Value] = v
	}
	return out, nil
}
func literal(n *yaml.Node, label string) (string, error) {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return "", problem(n, label+" must be an explicit string")
	}
	// Compose interpolation and escaping require a separate semantics contract.
	if strings.Contains(n.Value, "$") {
		return "", problem(n, label+" uses unsupported dollar interpolation")
	}
	return n.Value, nil
}
func parseService(n *yaml.Node) (service, error) {
	var s service
	m, err := mapping(n, "service", "image", "environment", "ports", "volumes")
	if err != nil {
		return s, err
	}
	s.image, err = literal(m["image"], "image")
	if err != nil {
		return s, err
	}
	if strings.TrimSpace(s.image) == "" {
		return s, problem(m["image"], "image must not be empty")
	}
	if e := m["environment"]; e != nil {
		if e.Kind != yaml.MappingNode {
			return s, problem(e, "environment must be a mapping of strings")
		}
		s.env = map[string]string{}
		for i := 0; i < len(e.Content); i += 2 {
			k, v := e.Content[i], e.Content[i+1]
			if !envName.MatchString(k.Value) {
				return s, problem(k, "invalid environment variable name")
			}
			value, err := literal(v, "environment value")
			if err != nil {
				return s, err
			}
			s.env[k.Value] = value
		}
	}
	if ports := m["ports"]; ports != nil {
		if ports.Kind != yaml.SequenceNode {
			return s, problem(ports, "ports must be a list of quoted mappings")
		}
		for _, p := range ports.Content {
			v, err := literal(p, "port")
			if err != nil {
				return s, err
			}
			if err = validatePort(v); err != nil {
				return s, problem(p, "unsupported port mapping")
			}
			s.ports = append(s.ports, v)
		}
	}
	if volumes := m["volumes"]; volumes != nil {
		if volumes.Kind != yaml.SequenceNode {
			return s, problem(volumes, "volumes must be a list of explicit bind mounts")
		}
		for _, v := range volumes.Content {
			fields, err := mapping(v, "mount", "type", "source", "target", "read_only", "bind")
			if err != nil {
				return s, err
			}
			kind, err := literal(fields["type"], "mount type")
			if err != nil || kind != "bind" {
				return s, problem(v, "only explicit bind mounts are supported")
			}
			// Compose defaults create_host_path to true, whereas Dill's engine
			// bind mounts do not create missing source directories. Require the
			// explicit Compose setting that matches Dill's behavior.
			bind, err := mapping(fields["bind"], "bind options", "create_host_path")
			if err != nil {
				return s, problem(v, "bind mounts require bind.create_host_path: false")
			}
			create := bind["create_host_path"]
			if create == nil || create.Kind != yaml.ScalarNode || create.Tag != "!!bool" || create.Value != "false" {
				return s, problem(create, "bind.create_host_path must be false")
			}
			src, err := literal(fields["source"], "mount source")
			if err != nil {
				return s, err
			}
			dst, err := literal(fields["target"], "mount target")
			if err != nil {
				return s, err
			}
			if !path.IsAbs(src) || path.Clean(src) != src || !path.IsAbs(dst) || path.Clean(dst) != dst {
				return s, problem(v, "bind paths must be canonical absolute paths")
			}
			ro := false
			if node := fields["read_only"]; node != nil {
				if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
					return s, problem(node, "read_only must be a boolean")
				}
				ro, err = strconv.ParseBool(node.Value)
				if err != nil {
					return s, problem(node, "invalid boolean")
				}
			}
			s.mounts = append(s.mounts, mount{src, dst, ro})
		}
	}
	return s, nil
}
func validatePort(p string) error {
	base := p
	if slash := strings.LastIndexByte(p, '/'); slash >= 0 {
		if p[slash+1:] != "tcp" && p[slash+1:] != "udp" {
			return fmt.Errorf("protocol")
		}
		base = p[:slash]
	}
	parts := strings.Split(base, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("parts")
	}
	if len(parts) == 3 {
		ip := net.ParseIP(parts[0])
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("host")
		}
		parts = parts[1:]
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != part {
			return fmt.Errorf("port")
		}
	}
	raw, _ := json.Marshal(p)
	_, err := config.NormalizePorts([]json.RawMessage{raw})
	return err
}
func pklString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&b, `\u{%x}`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
func render(schema string, services map[string]service) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "amends %s\n\nconfig {\n  services {\n", pklString(schema))
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := services[name]
		fmt.Fprintf(&b, "    [%s] {\n      image = %s\n", pklString(name), pklString(s.image))
		if len(s.env) > 0 {
			b.WriteString("      environment {\n")
			keys := make([]string, 0, len(s.env))
			for k := range s.env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(&b, "        [%s] = %s\n", pklString(k), pklString(s.env[k]))
			}
			b.WriteString("      }\n")
		}
		if len(s.ports) > 0 {
			b.WriteString("      ports = List(")
			for i, p := range s.ports {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(pklString(p))
			}
			b.WriteString(")\n")
		}
		if len(s.mounts) > 0 {
			b.WriteString("      volumes = List(\n")
			for _, m := range s.mounts {
				fmt.Fprintf(&b, "        new Mount { type = \"bind\"; source = %s; target = %s; readOnly = %t },\n", pklString(m.source), pklString(m.target), m.readOnly)
			}
			b.WriteString("      )\n")
		}
		b.WriteString("    }\n")
	}
	b.WriteString("  }\n}\n")
	return []byte(b.String())
}
