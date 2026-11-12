//go:build integration

package secretfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

// Only fixed schema spellings may enter logs. Dynamic map keys (including
// container/image/service names, environment/label keys and private paths) are
// replaced with [*]. Scalars are compared but never formatted or hashed.
func alphaPreviewChangedFields(before, after []byte) ([]string, error) {
	decode := func(b []byte) (any, error) {
		var v any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		err := d.Decode(&v)
		return v, err
	}
	a, err := decode(before)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	b, err := decode(after)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	allowed := map[string]bool{}
	for _, field := range strings.Fields(`Authority Configuration Live Plan Target Stack Engine Socket Services NoDeps Policy Bindings Mounts Sources
		Device Inode Size MTime CTime Seconds Nanoseconds UID GID Mode Links
		Containers Images States Resources OwnedVolumes ID Image ImageID Env Ports Volumes RestartPolicy UserLabels SystemLabels NetworkMode User HealthTest Command CapAdd Init Cpuset MountsKnown UserNSMode UIDMap GIDMap Memory MemoryReservation NanoCPUs CPUShares PidsLimit
		Kind Name Exists Settings Service Labels Options ForceRecreate DryRun Timeout Secrets
		Type Source HostPath ReadOnly HostIP Published Protocol HostUID HostGID HostMode Revision
		version targets id engine socket stacks name sources revision kind path uid gid mode target stack bindings source secrets services image environment ports volumes dependsOn command user labels healthCheck restartPolicy capAdd init cpuset memory memoryReservation cpus cpuShares pidsLimit containerName pullPolicy
		changes service diffs field before after
		name id driver network_interface created subnets subnet gateway lease_range start_ip end_ip ipv6_enabled internal dns_enabled network_dns_servers labels options ipam_options`) {
		allowed[field] = true
	}
	found := map[string]bool{}
	add := func(path string) {
		if len(found) < 64 {
			found[path] = true
		}
	}
	var walk func(any, any, string, int)
	walk = func(a, b any, path string, depth int) {
		if reflect.DeepEqual(a, b) {
			return
		}
		if depth >= 24 || len(path) >= 220 {
			add(path + ".[bounded]")
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			ordered := make([]string, 0, len(keys))
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			dynamic := path == "$.Live.Containers" || path == "$.Live.Images" || path == "$.Live.States" || path == "$.Authority.Mounts" || path == "$.Authority.Sources" || path == "$.Configuration.Services"
			for _, suffix := range []string{".Env", ".environment", ".UserLabels", ".SystemLabels", ".Labels", ".labels", ".Settings.Options", ".options", ".ipam_options"} {
				dynamic = dynamic || strings.HasSuffix(path, suffix)
			}
			for _, k := range ordered {
				next := path + "[*]"
				if !dynamic && allowed[k] {
					next = path + "." + k
				}
				av, ap := am[k]
				bv, bp := bm[k]
				if ap != bp {
					add(next)
				} else {
					walk(av, bv, next, depth+1)
				}
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok && len(aa) == len(ba) {
			for i := range aa {
				walk(aa[i], ba[i], fmt.Sprintf("%s[%d]", path, i), depth+1)
			}
			return
		}
		add(path)
	}
	walk(a, b, "$", 0)
	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func TestAlphaPreviewDiagnosticChangedFields(t *testing.T) {
	before := []byte(`{"Authority":{"Sources":{"private-service":[{"Device":1,"Inode":1,"Size":7,"CTime":{"Seconds":1,"Nanoseconds":2},"Links":1,"MTime":{"Seconds":1},"UID":1,"GID":1,"Mode":256}]}},"Live":{"Containers":{"private-container":{"ID":"old-private-id","Env":{"Name":"private-value"}}},"States":{"private-container":"running"}}}`)
	after := []byte(strings.NewReplacer(`"Device":1`, `"Device":2`, `"Size":7`, `"Size":8`, `"Seconds":1`, `"Seconds":2`, `"Inode":1`, `"Inode":2`, `"Nanoseconds":2`, `"Nanoseconds":3`, `"Links":1`, `"Links":2`, `"UID":1`, `"UID":2`, `"GID":1`, `"GID":2`, `"Mode":256`, `"Mode":288`, `"old-private-id"`, `"new-private-id"`, `"private-value"`, `"changed-private-value"`, `"running"`, `"exited"`).Replace(string(before)))
	got, err := alphaPreviewChangedFields(before, after)
	want := []string{"$.Authority.Sources[*][0].CTime.Nanoseconds", "$.Authority.Sources[*][0].CTime.Seconds", "$.Authority.Sources[*][0].Device", "$.Authority.Sources[*][0].GID", "$.Authority.Sources[*][0].Inode", "$.Authority.Sources[*][0].Links", "$.Authority.Sources[*][0].MTime.Seconds", "$.Authority.Sources[*][0].Mode", "$.Authority.Sources[*][0].Size", "$.Authority.Sources[*][0].UID", "$.Live.Containers[*].Env[*]", "$.Live.Containers[*].ID", "$.Live.States[*]"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("incorrect changed-field classification")
	}
	if strings.Contains(strings.Join(got, " "), "private") {
		t.Fatal("private scalar or dynamic key entered diagnostic")
	}
	unchanged, err := alphaPreviewChangedFields(before, before)
	if err != nil || len(unchanged) != 0 {
		t.Fatal("stable facts reported as changed")
	}
	many := make([]int, 1000)
	for i := range many {
		many[i] = 1
	}
	beforeMany, _ := json.Marshal(make([]int, len(many)))
	data, _ := json.Marshal(many)
	bounded, err := alphaPreviewChangedFields(beforeMany, data)
	if err != nil || len(bounded) != 64 || len(strings.Join(bounded, " ")) > 16000 {
		t.Fatal("unbounded changed-field diagnostic")
	}
}
