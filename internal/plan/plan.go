package plan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type ChangeKind int

const (
	KindCreate   ChangeKind = iota // container doesn't exist yet
	KindRecreate                   // container exists but config differs
	KindRemove                     // container exists but not in desired config
	KindNoop                       // container exists and config matches
)

func (k ChangeKind) MarshalJSON() ([]byte, error) {
	switch k {
	case KindCreate:
		return []byte(`"create"`), nil
	case KindRecreate:
		return []byte(`"recreate"`), nil
	case KindRemove:
		return []byte(`"remove"`), nil
	default:
		return []byte(`"noop"`), nil
	}
}

type FieldDiff struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type Change struct {
	Service string      `json:"service"`
	Kind    ChangeKind  `json:"kind"`
	Diffs   []FieldDiff `json:"diffs"`
}

type Plan struct {
	Changes []Change `json:"changes"`
}

func Compute(ctx context.Context, cfg *config.DillConfig, engine orchestrator.Engine, stackName string) (*Plan, error) {
	// Build desired map: serviceName → containerName.
	type desired struct {
		svc           *config.Service
		containerName string
	}
	desiredMap := make(map[string]desired, len(cfg.Services))
	for name, svc := range cfg.Services {
		cn := svc.ContainerName
		if cn == "" {
			cn = stackName + "_" + name
		}
		desiredMap[name] = desired{svc: svc, containerName: cn}
	}

	var changes []Change

	// For each desired service, compare against live state.
	for name, d := range desiredMap {
		live, err := engine.InspectConfig(ctx, d.containerName)
		if err != nil {
			if isNotFound(err) {
				changes = append(changes, Change{Service: name, Kind: KindCreate, Diffs: []FieldDiff{}})
				continue
			}
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}

		diffs, err := diffConfigs(name, d.svc, live)
		if err != nil {
			return nil, fmt.Errorf("diff %s: %w", name, err)
		}
		if len(diffs) == 0 {
			changes = append(changes, Change{Service: name, Kind: KindNoop})
		} else {
			changes = append(changes, Change{Service: name, Kind: KindRecreate, Diffs: diffs})
		}
	}

	// Find containers in the live stack that are not in the desired config.
	liveNames, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return nil, fmt.Errorf("list stack: %w", err)
	}

	// Build set of desired container names for O(1) lookup.
	desiredContainers := make(map[string]string, len(desiredMap)) // containerName → serviceName
	for svcName, d := range desiredMap {
		desiredContainers[d.containerName] = svcName
	}

	for _, cn := range liveNames {
		if _, ok := desiredContainers[cn]; !ok {
			changes = append(changes, Change{Service: cn, Kind: KindRemove, Diffs: []FieldDiff{}})
		}
	}

	// Sort for deterministic output.
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Service < changes[j].Service
	})

	return &Plan{Changes: changes}, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, orchestrator.ErrNotFound)
}

// diffConfigs compares desired service config against live container config
// and returns a list of field-level diffs.
func diffConfigs(name string, svc *config.Service, live *orchestrator.LiveConfig) ([]FieldDiff, error) {
	var diffs []FieldDiff

	if wantHash, err := config.ServiceConfigHash(name, svc); err != nil {
		return nil, fmt.Errorf("hashing desired config: %w", err)
	} else if gotHash := live.SystemLabels["dill.config-hash"]; gotHash != "" && gotHash != wantHash {
		diffs = append(diffs, FieldDiff{Field: "config_hash", Before: gotHash, After: wantHash})
	}

	// image
	if svc.Image != live.Image {
		diffs = append(diffs, FieldDiff{Field: "image", Before: live.Image, After: svc.Image})
	}

	// env — only keys defined in desired config
	for k, want := range svc.Environment {
		got := live.Env[k]
		if want != got {
			before, after := got, want
			if isSensitiveKey(k) {
				before, after = redactValue(got), redactValue(want)
			}
			diffs = append(diffs, FieldDiff{Field: "env." + k, Before: before, After: after})
		}
	}

	// ports
	desiredPorts, err := config.NormalizePorts(svc.Ports)
	if err != nil {
		return nil, fmt.Errorf("normalizing ports: %w", err)
	}
	diffs = append(diffs, diffPorts(desiredPorts, live.Ports)...)

	// volumes
	desiredVols, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("normalizing volumes: %w", err)
	}
	diffs = append(diffs, diffVolumes(desiredVols, live.Volumes)...)

	// restart policy
	wantRestart := normalizeRestart(svc.Restart)
	gotRestart := normalizeRestart(live.RestartPolicy)
	if wantRestart != gotRestart {
		diffs = append(diffs, FieldDiff{Field: "restart", Before: live.RestartPolicy, After: svc.Restart})
	}

	// network_mode — treat "", "bridge", "default" as equivalent.
	// Docker sets NetworkMode="bridge" on containers attached to a user-defined
	// network even when the user didn't specify a mode.
	if normalizeNetworkMode(svc.NetworkMode) != normalizeNetworkMode(live.NetworkMode) {
		diffs = append(diffs, FieldDiff{Field: "network_mode", Before: live.NetworkMode, After: svc.NetworkMode})
	}

	// user
	if svc.User != live.User {
		diffs = append(diffs, FieldDiff{Field: "user", Before: live.User, After: svc.User})
	}

	// health_test
	if svc.HealthCheck != nil {
		test, err := config.NormalizeHealthCheckTest(svc.HealthCheck.Test)
		if err == nil {
			wantHealth := strings.Join(test, " ")
			gotHealth := strings.Join(live.HealthTest, " ")
			if wantHealth != gotHealth {
				diffs = append(diffs, FieldDiff{Field: "health_test", Before: gotHealth, After: wantHealth})
			}
		}
	} else if len(live.HealthTest) > 0 {
		diffs = append(diffs, FieldDiff{Field: "health_test", Before: strings.Join(live.HealthTest, " "), After: ""})
	}

	// command — only diff when desired config specifies one; empty means "use image default"
	if len(svc.Command) > 0 {
		wantCmd := strings.Join(svc.Command, " ")
		gotCmd := strings.Join(live.Command, " ")
		if wantCmd != gotCmd {
			diffs = append(diffs, FieldDiff{Field: "command", Before: gotCmd, After: wantCmd})
		}
	}

	// cap_add — order-independent set comparison
	if !stringSetsEqual(svc.CapAdd, live.CapAdd) {
		diffs = append(diffs, FieldDiff{
			Field:  "cap_add",
			Before: strings.Join(sortedStrings(live.CapAdd), ","),
			After:  strings.Join(sortedStrings(svc.CapAdd), ","),
		})
	}

	// init
	wantInit := svc.Init != nil && *svc.Init
	if wantInit != live.Init {
		diffs = append(diffs, FieldDiff{
			Field:  "init",
			Before: formatBool(live.Init),
			After:  formatBool(wantInit),
		})
	}

	// cpuset
	if svc.Cpuset != live.Cpuset {
		diffs = append(diffs, FieldDiff{Field: "cpuset", Before: live.Cpuset, After: svc.Cpuset})
	}

	// user_labels — only check keys present in desired config.
	// Live-only labels are skipped: they may be image-level labels baked into
	// the image (e.g. nginx's MAINTAINER label) that dill never set and cannot
	// remove. Flagging them as "will be removed" is a false positive since they
	// reappear on every recreate regardless.
	for k, want := range svc.Labels {
		got, exists := live.UserLabels[k]
		if !exists {
			diffs = append(diffs, FieldDiff{Field: "label." + k, Before: "", After: want})
		} else if want != got {
			diffs = append(diffs, FieldDiff{Field: "label." + k, Before: got, After: want})
		}
	}

	// Sort diffs for deterministic output.
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Field < diffs[j].Field
	})

	return diffs, nil
}

func isSensitiveKey(key string) bool {
	k := strings.ToUpper(key)
	for _, marker := range []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "API_KEY", "ACCESS_KEY", "PRIVATE_KEY", "CREDENTIAL"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func redactValue(v string) string {
	if v == "" {
		return ""
	}
	return "[redacted]"
}

func normalizeRestart(r string) string {
	if r == "" || r == "no" {
		return "no"
	}
	return r
}

// normalizeNetworkMode treats Docker's default bridge mode representations as
// equivalent to an unset network mode in the desired config.
func normalizeNetworkMode(m string) string {
	if m == "" || m == "bridge" || m == "default" {
		return ""
	}
	return m
}

// normalizeHostIP treats the various "any interface" representations as equal
// so that Docker's default "::" binding doesn't diff against an unset HostIP.
func normalizeHostIP(ip string) string {
	if ip == "" || ip == "0.0.0.0" || ip == "::" {
		return ""
	}
	return ip
}

// diffPorts returns FieldDiff entries for port binding changes.
func diffPorts(desired, live []config.PortBinding) []FieldDiff {
	key := func(p config.PortBinding) string {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		return fmt.Sprintf("%s/%s", p.Target, proto)
	}

	// normPortStr is used for semantic comparison only (not display).
	// It normalises HostIP so that "" / "0.0.0.0" / "::" are treated the same.
	normPortStr := func(p config.PortBinding) string {
		p.HostIP = normalizeHostIP(p.HostIP)
		return portStr(p)
	}

	desiredSet := make(map[string]config.PortBinding, len(desired))
	for _, p := range desired {
		desiredSet[key(p)] = p
	}
	liveSet := make(map[string]config.PortBinding, len(live))
	for _, p := range live {
		liveSet[key(p)] = p
	}

	var diffs []FieldDiff
	for k, dp := range desiredSet {
		lp, exists := liveSet[k]
		if !exists {
			diffs = append(diffs, FieldDiff{Field: "port." + k, Before: "", After: portStr(dp)})
		} else if normPortStr(dp) != normPortStr(lp) {
			diffs = append(diffs, FieldDiff{Field: "port." + k, Before: portStr(lp), After: portStr(dp)})
		}
	}
	for k, lp := range liveSet {
		if _, exists := desiredSet[k]; !exists {
			// Skip ports with no host binding — they are image EXPOSE entries
			// injected by the engine, not explicitly configured by the user.
			if lp.Published == "" {
				continue
			}
			diffs = append(diffs, FieldDiff{Field: "port." + k, Before: portStr(lp), After: ""})
		}
	}
	return diffs
}

func portStr(p config.PortBinding) string {
	if p.Published == "" {
		return p.Target
	}
	if p.HostIP != "" {
		return fmt.Sprintf("%s:%s->%s", p.HostIP, p.Published, p.Target)
	}
	return fmt.Sprintf("%s->%s", p.Published, p.Target)
}

// diffVolumes returns FieldDiff entries for volume mount changes.
func diffVolumes(desired, live []config.VolumeMount) []FieldDiff {
	key := func(v config.VolumeMount) string { return v.Target }

	desiredSet := make(map[string]config.VolumeMount, len(desired))
	for _, v := range desired {
		desiredSet[key(v)] = v
	}
	liveSet := make(map[string]config.VolumeMount, len(live))
	for _, v := range live {
		liveSet[key(v)] = v
	}

	var diffs []FieldDiff
	for k, dv := range desiredSet {
		lv, exists := liveSet[k]
		if !exists {
			diffs = append(diffs, FieldDiff{Field: "volume." + k, Before: "", After: volStr(dv)})
		} else if volStr(dv) != volStr(lv) {
			diffs = append(diffs, FieldDiff{Field: "volume." + k, Before: volStr(lv), After: volStr(dv)})
		}
	}
	for range liveSet {
		// Live-only volumes are not flagged as diffs: the engine may inject
		// anonymous volumes for targets declared in the image's VOLUME directive
		// that the user never configured. We can't distinguish those from
		// volumes the user intentionally removed without storing prior state.
	}
	return diffs
}

func volStr(v config.VolumeMount) string {
	// Include Type so that changing a named volume to a bind mount (or vice
	// versa) is detected even when source and target strings are the same.
	t := v.Type
	if t == "" {
		t = "volume"
	}
	s := t + ":" + v.Source + ":" + v.Target
	if v.ReadOnly {
		s += ":ro"
	}
	return s
}

// stringSetsEqual reports whether a and b contain the same strings (order-independent).
func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]int, len(a))
	for _, s := range a {
		set[s]++
	}
	for _, s := range b {
		set[s]--
		if set[s] < 0 {
			return false
		}
	}
	return true
}

// sortedStrings returns a sorted copy of ss.
func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

func formatBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
