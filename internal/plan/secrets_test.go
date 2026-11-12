package plan

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func secretDiffFixture(t *testing.T) (*config.Service, []config.ResolvedSecret, *orchestrator.LiveConfig) {
	t.Helper()
	s := &config.Service{Image: "image", Secrets: []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256}}}
	refs := []config.ResolvedSecret{{Name: "db", Source: "db-prod", Revision: "r1", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256, HostPath: "/synthetic-private/source-r1", HostUID: 1000, HostGID: 1000, HostMode: 256}}
	hash, err := config.SecretServiceConfigHash("demo", "app", s, "lab", refs)
	if err != nil {
		t.Fatal(err)
	}
	live := &orchestrator.LiveConfig{Image: "image", MountsKnown: true, SystemLabels: map[string]string{"dill.config-hash": hash, orchestrator.SecretEnabledLabel: "true"}, Volumes: []config.VolumeMount{{Type: "bind", Source: refs[0].HostPath, Target: refs[0].Target, ReadOnly: true}}}
	return s, refs, live
}
func TestSecretInspectNoopAndDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*orchestrator.LiveConfig)
		want   bool
	}{
		{"noop", func(*orchestrator.LiveConfig) {}, false},
		{"missing", func(l *orchestrator.LiveConfig) { l.Volumes = nil }, true},
		{"source", func(l *orchestrator.LiveConfig) { l.Volumes[0].Source = "/synthetic-private/wrong" }, true},
		{"writable", func(l *orchestrator.LiveConfig) { l.Volumes[0].ReadOnly = false }, true},
		{"type", func(l *orchestrator.LiveConfig) { l.Volumes[0].Type = "volume" }, true},
		{"destination", func(l *orchestrator.LiveConfig) { l.Volumes[0].Target = "/run/secrets/other" }, true},
		{"extra", func(l *orchestrator.LiveConfig) {
			l.Volumes = append(l.Volumes, config.VolumeMount{Type: "bind", Source: "/synthetic-private/extra", Target: "/run/secrets/extra"})
		}, true},
		{"extra alias elsewhere", func(l *orchestrator.LiveConfig) {
			extra := l.Volumes[0]
			extra.Target = "/elsewhere"
			l.Volumes = append(l.Volumes, extra)
		}, true},
		{"extra unknown bind elsewhere", func(l *orchestrator.LiveConfig) {
			l.Volumes = append(l.Volumes, config.VolumeMount{Type: "bind", Source: "/synthetic-private/old-secret", Target: "/elsewhere", ReadOnly: true})
		}, true},
		{"ancestor masks target", func(l *orchestrator.LiveConfig) {
			l.Volumes = append(l.Volumes, config.VolumeMount{Type: "bind", Source: "/synthetic-private/ancestor", Target: "/run"})
		}, true},
		{"duplicate", func(l *orchestrator.LiveConfig) { l.Volumes = append(l.Volumes, l.Volumes[0]) }, true},
		{"missing marker", func(l *orchestrator.LiveConfig) { delete(l.SystemLabels, orchestrator.SecretEnabledLabel) }, true},
		{"bad marker", func(l *orchestrator.LiveConfig) { l.SystemLabels[orchestrator.SecretEnabledLabel] = "false" }, true},
		{"old hash", func(l *orchestrator.LiveConfig) { l.SystemLabels["dill.config-hash"] = "old" }, true},
		{"missing hash", func(l *orchestrator.LiveConfig) { delete(l.SystemLabels, "dill.config-hash") }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, refs, live := secretDiffFixture(t)
			tc.change(live)
			diffs, err := diffConfigsChecked("app", s, live, "", "demo", "lab", refs)
			if err != nil {
				t.Fatal(err)
			}
			if (len(diffs) > 0) != tc.want {
				t.Fatalf("diffs=%v", diffs)
			}
			b, _ := json.Marshal(diffs)
			if strings.Contains(string(b), "synthetic-private") {
				t.Fatalf("private inspect path rendered: %s", b)
			}
		})
	}
	s, refs, live := secretDiffFixture(t)
	live.MountsKnown = false
	if _, err := diffConfigsChecked("app", s, live, "", "demo", "lab", refs); err != config.ErrSecretUnavailable {
		t.Fatal("uninspectable mount metadata accepted")
	}
}
func TestSecretRotationAndRemoval(t *testing.T) {
	s, refs, live := secretDiffFixture(t)
	refs[0].Revision = "r2"
	refs[0].HostPath = "/synthetic-private/source-r2"
	diffs, err := diffConfigsChecked("app", s, live, "", "demo", "lab", refs)
	if err != nil || len(diffs) < 2 {
		t.Fatalf("rotation must change reference and mount: %v %v", diffs, err)
	}
	s.Secrets = nil
	diffs, err = diffConfigsChecked("app", s, live, "", "demo", "", nil)
	if err != nil || len(diffs) == 0 {
		t.Fatal("removing grants did not recreate")
	}
	b, _ := json.Marshal(diffs)
	if strings.Contains(string(b), "synthetic-private") {
		t.Fatal("removed source path leaked")
	}
}

func TestSecretMovedMountCannotLeakThroughOrdinaryVolumeDiff(t *testing.T) {
	svc, refs, live := secretDiffFixture(t)
	svc.Volumes = []json.RawMessage{json.RawMessage(`"/public/data:/data"`)}
	live.Volumes = append(live.Volumes, config.VolumeMount{Type: "bind", Source: "/synthetic-private/old-secret", Target: "/data"})
	diffs, err := diffConfigsChecked("app", svc, live, "", "demo", "lab", refs)
	if err != nil || len(diffs) == 0 {
		t.Fatalf("moved mount drift missing: %v", err)
	}
	b, _ := json.Marshal(diffs)
	if strings.Contains(string(b), "synthetic-private") {
		t.Fatal("private old source escaped via ordinary volume diff")
	}
}
