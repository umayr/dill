package plan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/secretfile"
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
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return nil, config.ErrSecretUnavailable
	}
	return compute(ctx, cfg, engine, stackName, nil, nil, "")
}

// ComputeChecked consumes opaque point-in-time evidence and only inspects the
// requested closure. Orphan detection still uses the full declared config.
func ComputeChecked(ctx context.Context, cfg *config.DillConfig, engine orchestrator.Engine, stackName string, req secretfile.Request, result *secretfile.Result) (p *Plan, retErr error) {
	defer func() {
		if retErr != nil {
			retErr = config.ErrSecretUnavailable
		}
	}()
	endpoint, ok := engine.(interface{ CLIEndpoint() string })
	if req.Config != cfg || stackName != req.Stack || cfg == nil || cfg.Engine != req.Engine || !ok || endpoint.CLIEndpoint() != "unix://"+req.Socket {
		return nil, config.ErrSecretUnavailable
	}
	mounts, err := result.Mounts(req)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	var add func(string)
	add = func(name string) {
		if selected[name] {
			return
		}
		selected[name] = true
		if !req.NoDeps {
			for _, dep := range cfg.Services[name].DependsOn {
				add(dep)
			}
		}
	}
	if len(req.Services) == 0 {
		for name := range cfg.Services {
			selected[name] = true
		}
	} else {
		for _, name := range req.Services {
			add(name)
		}
	}
	return compute(ctx, cfg, engine, stackName, selected, mounts, req.Target)
}

func compute(ctx context.Context, cfg *config.DillConfig, engine orchestrator.Engine, stackName string, selected map[string]bool, mounts map[string][]config.ResolvedSecret, target string) (*Plan, error) {
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
		if selected != nil && !selected[name] {
			continue
		}
		live, err := engine.InspectConfig(ctx, d.containerName)
		if err != nil {
			if isNotFound(err) {
				changes = append(changes, Change{Service: name, Kind: KindCreate, Diffs: []FieldDiff{}})
				continue
			}
			return nil, fmt.Errorf("inspect %s: %w", name, err)
		}

		if err := orchestrator.CheckOwnership(d.containerName, stackName, name, live); err != nil {
			return nil, err
		}

		localImageID, err := engine.LocalImageID(ctx, d.svc.Image)
		if err != nil {
			return nil, fmt.Errorf("resolving local image for %s: %w", name, err)
		}

		diffs, err := diffConfigsChecked(name, d.svc, live, localImageID, stackName, target, mounts[name])
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
func diffConfigs(name string, svc *config.Service, live *orchestrator.LiveConfig, localImageID, stackName string) ([]FieldDiff, error) {
	return diffConfigsChecked(name, svc, live, localImageID, stackName, "", nil)
}
func diffConfigsChecked(name string, svc *config.Service, live *orchestrator.LiveConfig, localImageID, stackName, target string, secrets []config.ResolvedSecret) ([]FieldDiff, error) {
	var diffs []FieldDiff

	wantHash, err := config.SecretServiceConfigHash(stackName, name, svc, target, secrets)
	if err != nil {
		return nil, fmt.Errorf("hashing desired config: %w", err)
	}
	gotHash := live.SystemLabels["dill.config-hash"]
	if (gotHash != "" || len(svc.Secrets) > 0) && gotHash != wantHash {
		if len(svc.Secrets) > 0 {
			diffs = append(diffs, FieldDiff{Field: "secret_references", Before: "previous", After: "changed"})
			for _, r := range secrets {
				diffs = append(diffs, FieldDiff{Field: "secret." + r.Name, Before: "previous", After: r.Revision})
			}
		} else {
			diffs = append(diffs, FieldDiff{Field: "config_hash", Before: gotHash, After: wantHash})
		}
	}

	// Guarded creates use a metadata-checked immutable ID, but the desired
	// hash deliberately retains the original image reference. Accept that
	// representation only with matching desired provenance, an actual pinned
	// live content ID, and known desired content. Reference/config changes
	// must not be hidden merely because two tags resolve to the same image.
	pinnedReference := gotHash != "" && gotHash == wantHash &&
		immutableImageID(live.Image) && immutableImageID(live.ImageID) && immutableImageID(localImageID) &&
		normalizeImageID(live.Image) == normalizeImageID(live.ImageID)
	if svc.Image != live.Image && !pinnedReference {
		diffs = append(diffs, FieldDiff{Field: "image", Before: live.Image, After: svc.Image})
	} else if want, got := normalizeImageID(localImageID), normalizeImageID(live.ImageID); want != "" && got != "" && want != got {
		// Same tag, different local image: the registry tag moved or the image
		// was rebuilt locally. Recreate so the container picks it up.
		diffs = append(diffs, FieldDiff{Field: "image_id", Before: shortImageID(got), After: shortImageID(want)})
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
	desiredVols = config.ResolveVolumes(desiredVols, stackName)
	liveVolumes := live.Volumes
	_, marked := live.SystemLabels[orchestrator.SecretEnabledLabel]
	if len(svc.Secrets) > 0 || marked {
		if !live.MountsKnown || (live.UserNSMode != "" && live.UserNSMode != "host") || len(live.UIDMap) > 0 || len(live.GIDMap) > 0 {
			return nil, config.ErrSecretUnavailable
		}
		ordinary, changed := secretMountDrift(secrets, desiredVols, live.Volumes)
		liveVolumes = ordinary
		if changed || (len(svc.Secrets) > 0 && live.SystemLabels[orchestrator.SecretEnabledLabel] != "true") || (len(svc.Secrets) == 0 && marked) {
			diffs = append(diffs, FieldDiff{Field: "secret_mounts", Before: "previous", After: "checked configuration"})
		}
	}
	volumeDiffs := diffVolumes(desiredVols, liveVolumes)
	if len(svc.Secrets) > 0 || marked {
		// A stale/moved secret bind can occupy an ordinary target. Never render
		// any live mount source inventory from a secret-marked service.
		for i := range volumeDiffs {
			if volumeDiffs[i].Before != "" {
				volumeDiffs[i].Before = "previous mount"
			}
			if volumeDiffs[i].After != "" {
				volumeDiffs[i].After = "configured mount"
			}
		}
	}
	diffs = append(diffs, volumeDiffs...)

	// restart policy
	wantRestart := normalizeRestart(svc.Restart)
	gotRestart := normalizeRestart(live.RestartPolicy)
	if wantRestart != gotRestart {
		diffs = append(diffs, FieldDiff{Field: "restart", Before: live.RestartPolicy, After: svc.Restart})
	}

	// networkMode — treat "", "bridge", "default" as equivalent.
	// Docker sets NetworkMode="bridge" on containers attached to a user-defined
	// network even when the user didn't specify a mode.
	if normalizeNetworkMode(svc.NetworkMode) != normalizeNetworkMode(live.NetworkMode) {
		diffs = append(diffs, FieldDiff{Field: "networkMode", Before: live.NetworkMode, After: svc.NetworkMode})
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

	// capAdd — order-independent set comparison
	if !stringSetsEqual(svc.CapAdd, live.CapAdd) {
		diffs = append(diffs, FieldDiff{
			Field:  "capAdd",
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

	resources, err := config.NormalizeResources(svc)
	if err != nil {
		return nil, fmt.Errorf("normalizing resources: %w", err)
	}
	// Unset/zero resources defer to engine defaults (notably Podman's PID
	// limit). Compare only explicitly requested limits, as with environment
	// keys. Removing a previously configured limit is detected by config_hash.
	for _, field := range []struct {
		name string
		want int64
		got  int64
	}{
		{"memLimit", resources.Memory, live.Resources.Memory},
		{"memReservation", resources.MemoryReservation, live.Resources.MemoryReservation},
		{"cpus", resources.NanoCPUs, live.Resources.NanoCPUs},
		{"cpuShares", resources.CPUShares, live.Resources.CPUShares},
		{"pidsLimit", resources.PidsLimit, live.Resources.PidsLimit},
	} {
		if field.want == 0 {
			continue
		}
		// Engines represent unlimited PIDs as either zero or -1.
		if field.name == "pidsLimit" && field.want == -1 && field.got <= 0 {
			continue
		}
		if field.want != field.got {
			diffs = append(diffs, FieldDiff{
				Field:  field.name,
				Before: formatResourceValue(field.name, field.got),
				After:  formatResourceValue(field.name, field.want),
			})
		}
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

// Memory values are reported in bytes; CPUs use the same core units as Pkl.
func formatResourceValue(field string, value int64) string {
	if field == "cpus" {
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%d.%09d", value/1_000_000_000, value%1_000_000_000), "0"), ".")
	}
	return strconv.FormatInt(value, 10)
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

// normalizeImageID strips the algorithm prefix so Docker and Podman IDs compare.
func normalizeImageID(id string) string {
	return strings.TrimPrefix(id, "sha256:")
}

// immutableImageID distinguishes a full engine content ID from a tag, short
// prefix or missing evidence. Both adapters use SHA-256 content identities.
func immutableImageID(id string) bool {
	id = normalizeImageID(id)
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// shortImageID returns the first 12 hex characters of an image ID for display.
func shortImageID(id string) string {
	id = normalizeImageID(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
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

// Secret paths never flow into ordinary volume rendering, including drifted,
// writable, missing and extra mounts. Unknown mount metadata fails upstream.
func secretMountDrift(desired []config.ResolvedSecret, configured, live []config.VolumeMount) ([]config.VolumeMount, bool) {
	want := map[string]config.ResolvedSecret{}
	sources := map[string]bool{}
	for _, s := range desired {
		want[s.Target] = s
		sources[s.HostPath] = true
	}
	ordinaryTargets := map[string]bool{}
	for _, m := range configured {
		ordinaryTargets[m.Target] = true
	}
	ordinary := []config.VolumeMount{}
	seen := map[string]bool{}
	changed := false
	for _, m := range live {
		overlaps := false
		for t := range want {
			if m.Target == "/" || t == m.Target || strings.HasPrefix(t, m.Target+"/") || strings.HasPrefix(m.Target, t+"/") {
				overlaps = true
			}
		}
		if strings.HasPrefix(m.Target, "/run/secrets/") || m.Target == "/run/secrets" || sources[m.Source] || overlaps {
			s, ok := want[m.Target]
			if !ok || seen[m.Target] || m.Type != "bind" || m.Source != s.HostPath || !m.ReadOnly {
				changed = true
			}
			seen[m.Target] = true
		} else if m.Type == "bind" && !ordinaryTargets[m.Target] {
			// Unknown host-file binds are not image-injected anonymous volumes.
			changed = true
		} else {
			ordinary = append(ordinary, m)
		}
	}
	for t := range want {
		if !seen[t] {
			changed = true
		}
	}
	return ordinary, changed
}
