//go:build linux

package main

import (
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Post-apply diagnostic observes only authenticated evidence. It never creates a
// coordination session/receipt, acknowledges unknown, applies, or replays.
func TestBrokerRuntimePostApplyEvidence(t *testing.T) {
	f := os.Getenv("DILL_BROKER_RUNTIME_FIXTURE")
	profilePath := os.Getenv("BROKER_TEST_PROFILE")
	if f == "" || profilePath == "" {
		t.Skip("explicit fixture/profile required")
	}
	t.Setenv("DILL_PKL_PATH", filepath.Join(filepath.Dir(f), "pkl"))
	raw, err := os.ReadFile(filepath.Join(f, "prepared-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ref brokersecret.Reference
	if json.Unmarshal(raw, &ref) != nil {
		t.Fatal("invalid reference")
	}
	profile, err := coordination.ReadBrokerProfile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	sources, _ := filepath.Glob(filepath.Join(f, "stages", "*", "compose.pkl"))
	if len(sources) != 1 {
		t.Fatal("ambiguous source")
	}
	o := brokerCLIOptions{store: filepath.Join(f, "runtime"), file: sources[0], stack: ref.Scope.Stack, profilePath: profilePath, profile: &profile, socket: profile.Socket(), state: filepath.Join(f, "coord"), source: ref.Scope.SourceDigest}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, _, err := brokerCLIConfig(ctx, o, ref.Scope)
	if err != nil {
		t.Fatal("config", err)
	}
	store, err := brokersecret.Open(o.store)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m, err := store.Resolve(ref.Handle, ref.Scope)
	if err != nil {
		t.Fatal(err)
	}
	target, podman, err := profile.Qualify(ctx)
	if err != nil {
		t.Fatal("qualification", err)
	}
	defer target.Close()
	e, err := orchestrator.NewBrokerPodmanEngine(ctx, podman, cfg, m)
	if err != nil {
		t.Fatal("read adapter", err)
	}
	networks, err := e.PreviewResources(ctx, ref.Scope.Stack, nil)
	if err != nil || len(networks) != 1 {
		t.Fatal("network projection", err)
	}
	if _, err = preflight.BrokerResourceDigest(ref.Scope.Engine, ref.Scope.Stack, networks[0]); err != nil {
		t.Fatalf("network digest: %v; metadata=%+v", err, networks[0])
	}
	for name, svc := range cfg.Services {
		container := svc.ContainerName
		if container == "" {
			container = ref.Scope.Stack + "_" + name
		}
		live, err := e.InspectConfig(ctx, container)
		if err != nil {
			t.Fatal("live projection", err)
		}
		if err = orchestrator.CheckOwnership(container, ref.Scope.Stack, name, live); err != nil {
			t.Fatal("ownership", err)
		}
		if !live.NetworksKnown || len(live.Networks) != 1 || live.Networks[0] != ref.Scope.Stack {
			t.Fatalf("network membership known=%v networks=%v", live.NetworksKnown, live.Networks)
		}
		if err = orchestrator.CheckBrokerMounts(name, live, m); err != nil {
			t.Fatal("authenticated mounts", err)
		}
		t.Logf("authenticated live metadata valid; id=%s", live.ID)
	}
}
