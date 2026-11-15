package preflight

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func syntheticLive() *orchestrator.LiveConfig {
	return &orchestrator.LiveConfig{
		ID: strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), Image: "synthetic:reference", InspectedName: "synthetic_web",
		HostConfigKnown: true, MountsKnown: true, NetworksKnown: true, Networks: []string{"synthetic"},
		SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "synthetic", "dill.service": "web"},
	}
}
func TestContainerLeafActualIdentityAndKnownEvidence(t *testing.T) {
	live := syntheticLive()
	base, err := ContainerDigest("synthetic", "web", "synthetic_web", live)
	if err != nil || !ValidDigest(base) {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*orchestrator.LiveConfig){
		"lookup not actual":    func(v *orchestrator.LiveConfig) { v.InspectedName = "other" },
		"missing actual name":  func(v *orchestrator.LiveConfig) { v.InspectedName = "" },
		"short ID":             func(v *orchestrator.LiveConfig) { v.ID = "short" },
		"missing actual image": func(v *orchestrator.LiveConfig) { v.ImageID = "" },
		"unknown host":         func(v *orchestrator.LiveConfig) { v.HostConfigKnown = false },
		"unknown mounts":       func(v *orchestrator.LiveConfig) { v.MountsKnown = false },
		"unknown networks":     func(v *orchestrator.LiveConfig) { v.NetworksKnown = false },
		"foreign owner":        func(v *orchestrator.LiveConfig) { v.SystemLabels["dill.stack"] = "foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			v := syntheticLive()
			mutate(v)
			if _, err := ContainerDigest("synthetic", "web", "synthetic_web", v); !errors.Is(err, ErrEvidenceUnavailable) {
				t.Fatal("missing/foreign evidence became private fact", err)
			}
		})
	}
	if _, err = ContainerDigest("synthetic", "web", "synthetic_web", nil); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatal("nil evidence accepted")
	}
}
func TestContainerLeafNormalizationAndNoDesiredReferenceRewriting(t *testing.T) {
	live := syntheticLive()
	base, err := ContainerDigest("synthetic", "web", "synthetic_web", live)
	if err != nil {
		t.Fatal(err)
	}
	equivalent := syntheticLive()
	equivalent.RestartPolicy = "no"
	equivalent.NetworkMode = "bridge"
	equivalent.ImageID = strings.Repeat("b", 64)
	equivalent.Image = "different-spelling-not-actual-content"
	equivalent.Networks = []string{"synthetic", "synthetic"}
	equivalent.Env = map[string]string{}
	equivalent.UserLabels = map[string]string{}
	got, err := ContainerDigest("synthetic", "web", "synthetic_web", equivalent)
	if err != nil || got != base {
		t.Fatal("documented inspected normalization changed", err)
	}
	for name, mutate := range map[string]func(*orchestrator.LiveConfig){
		"actual image ID":    func(v *orchestrator.LiveConfig) { v.ImageID = "sha256:" + strings.Repeat("c", 64) },
		"container ID":       func(v *orchestrator.LiveConfig) { v.ID = strings.Repeat("c", 64) },
		"actual environment": func(v *orchestrator.LiveConfig) { v.Env = map[string]string{"SYNTHETIC": "different"} },
		"labels":             func(v *orchestrator.LiveConfig) { v.UserLabels = map[string]string{"synthetic": "different"} },
		"capability":         func(v *orchestrator.LiveConfig) { v.CapAdd = []string{"CHOWN"} },
		"host privilege":     func(v *orchestrator.LiveConfig) { v.Privileged = true },
		"resource limit":     func(v *orchestrator.LiveConfig) { v.Resources.Memory = 42 },
		"command":            func(v *orchestrator.LiveConfig) { v.Command = []string{"synthetic"} },
		"health test":        func(v *orchestrator.LiveConfig) { v.HealthTest = []string{"CMD", "synthetic"} },
	} {
		t.Run(name, func(t *testing.T) {
			v := syntheticLive()
			mutate(v)
			got, err := ContainerDigest("synthetic", "web", "synthetic_web", v)
			if err != nil || got == base {
				t.Fatal("actual fact not committed", err)
			}
		})
	}
	// This hashing helper records unsafe actual facts rather than blessing
	// them; the separate mandatory whole profile must reject such a workload.
	if live.Image != "synthetic:reference" || live.ImageID != "sha256:"+strings.Repeat("b", 64) {
		t.Fatal("original inspected evidence mutated")
	}
}
func TestContainerLeafPortMountOrderAndCompleteFacts(t *testing.T) {
	v := syntheticLive()
	v.Ports = []config.PortBinding{{HostIP: "0.0.0.0", Published: "8080", Target: "80"}, {HostIP: "127.0.0.1", Published: "8081", Target: "80", Protocol: "tcp"}}
	v.Volumes = []config.VolumeMount{{Type: "volume", Source: "synthetic_b", Target: "/b"}, {Type: "volume", Source: "synthetic_a", Target: "/a"}}
	base, err := ContainerDigest("synthetic", "web", "synthetic_web", v)
	if err != nil {
		t.Fatal(err)
	}
	u := syntheticLive()
	u.Ports = []config.PortBinding{v.Ports[1], v.Ports[0]}
	u.Ports[1].HostIP = "::"
	u.Ports[1].Protocol = "tcp"
	u.Volumes = []config.VolumeMount{v.Volumes[1], v.Volumes[0]}
	got, err := ContainerDigest("synthetic", "web", "synthetic_web", u)
	if err != nil || got != base {
		t.Fatal("port/mount order changed", err)
	}
	u.Ports = u.Ports[:1]
	got, err = ContainerDigest("synthetic", "web", "synthetic_web", u)
	if err != nil || got == base {
		t.Fatal("multiple bindings collapsed")
	}
	if !reflect.DeepEqual(v.Ports[0], config.PortBinding{HostIP: "0.0.0.0", Published: "8080", Target: "80"}) {
		t.Fatal("inspected port facts mutated")
	}
}
