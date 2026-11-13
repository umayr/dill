package preflight

import (
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func inputConfigFixture() *config.DillConfig {
	return &config.DillConfig{Engine: "docker", Services: map[string]*config.Service{"web": {Image: "synthetic-original:one", PullPolicy: "never", DependsOn: []string{"db"}}, "db": {Image: "synthetic-db:one", PullPolicy: "never"}}}
}
func TestConfigInputActualProvenanceDependenciesAndPreparedCoverage(t *testing.T) {
	cfg := inputConfigFixture()
	image := "sha256:" + strings.Repeat("a", 64)
	prepared := map[string]string{"web": image, "db": image}
	flags := sampleInput(t).Flags
	actual, err := ConfigInput(cfg, "synthetic", "up", flags, prepared)
	if err != nil {
		t.Fatal(err)
	}
	original, err := actual.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["web"].Image != "synthetic-original:one" || actual.Services[1].DependsOn[0] != "db" {
		t.Fatal("desired provenance/dependencies altered")
	}
	cfg.Services["web"].Image = "synthetic-original:two"
	changed, err := ConfigInput(cfg, "synthetic", "up", flags, prepared)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := changed.Digest()
	if err != nil || digest == original {
		t.Fatal("same prepared ID hid desired original-reference change")
	}
	cfg.Services["web"].Image = "synthetic-original:one"
	cfg.Services["web"].DependsOn = nil
	changed, err = ConfigInput(cfg, "synthetic", "up", flags, prepared)
	if err != nil {
		t.Fatal(err)
	}
	digest, err = changed.Digest()
	if err != nil || digest == original {
		t.Fatal("dependency-only change not bound")
	}
	cfg = inputConfigFixture()
	prepared["web"] = strings.Repeat("a", 64)
	changed, err = ConfigInput(cfg, "synthetic", "up", flags, prepared)
	if err != nil {
		t.Fatal(err)
	}
	digest, err = changed.Digest()
	if err != nil || digest != original {
		t.Fatal("full image ID representation not canonical")
	}
	for _, bad := range []map[string]string{nil, {"web": image}, {"web": image, "extra": image}, {"web": "short", "db": image}} {
		if _, err = ConfigInput(cfg, "synthetic", "up", flags, bad); err == nil {
			t.Fatal("missing/unverified prepared-image evidence accepted")
		}
	}
}
func TestConfigInputEmptyAndUnsupported(t *testing.T) {
	flags := sampleInput(t).Flags
	flags.NoPrune = false
	empty := &config.DillConfig{Engine: "docker", Services: map[string]*config.Service{}}
	v, err := ConfigInput(empty, "synthetic", "start", flags, map[string]string{})
	if err != nil || len(v.Services) != 0 {
		t.Fatal("valid empty input rejected", err)
	}
	cfg := inputConfigFixture()
	cfg.Services["web"].PullPolicy = "always"
	if _, err = ConfigInput(cfg, "synthetic", "up", flags, map[string]string{"web": strings.Repeat("a", 64), "db": strings.Repeat("a", 64)}); err == nil {
		t.Fatal("pull authority admitted into prepared profile")
	}
	if _, err = ConfigInput(nil, "synthetic", "up", flags, nil); err == nil {
		t.Fatal("nil config admitted")
	}
	inv := Invocation{Source: "brine", Timeout: 123, NoPrune: true, Quiet: true}
	if got := inv.EffectiveFlags(); got.TimeoutNanos != 123 || got.CallerSource != "brine" || got.EffectivePullPolicy != "never" || got.NoDeps {
		t.Fatal("effective flags not source-duration bound")
	}
}
