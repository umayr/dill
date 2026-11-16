package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/umayr/dill/core"
)

type planSource struct {
	calls int
	live  *core.LiveConfig
	list  []string
}

func (s *planSource) InspectConfig(context.Context, string) (*core.LiveConfig, error) {
	s.calls++
	if s.live == nil {
		return nil, core.ErrNotFound
	}
	return s.live, nil
}
func (s *planSource) LocalImageID(context.Context, string) (string, error) { s.calls++; return "", nil }
func (s *planSource) ListStack(context.Context, string) ([]string, error) {
	s.calls++
	return s.list, nil
}

func TestPublicPlanningConsumer(t *testing.T) {
	cfg := &core.Config{Services: map[string]*core.Service{"web": {Image: "example"}}}
	src := &planSource{list: []string{"old"}}
	got, err := core.ComputePlan(context.Background(), cfg, src, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 2 || got.Changes[0].Service != "old" || got.Changes[0].Kind != core.Remove || got.Changes[1].Service != "web" || got.Changes[1].Kind != core.Create {
		t.Fatalf("unexpected plan: %#v", got)
	}
}
func TestPlanningRejectsSecretsAndInvalidConfigBeforeEngine(t *testing.T) {
	src := &planSource{}
	cfg := &core.Config{Services: map[string]*core.Service{"web": {Secrets: []core.SecretGrant{{Name: "credential"}}}}}
	if _, err := core.ComputePlan(context.Background(), cfg, src, "demo"); !errors.Is(err, core.ErrSecretPlanningUnsupported) {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg.Services["web"].Secrets = nil
	cfg.Services["web"].Labels = map[string]string{"dill.managed": "true"}
	if _, err := core.ComputePlan(context.Background(), cfg, src, "demo"); err == nil {
		t.Fatal("expected invalid config")
	}
	if src.calls != 0 {
		t.Fatalf("engine accessed %d times", src.calls)
	}
}
func TestPlanningChecksOwnership(t *testing.T) {
	cfg := &core.Config{Services: map[string]*core.Service{"web": {Image: "example"}}}
	src := &planSource{live: &core.LiveConfig{}}
	if _, err := core.ComputePlan(context.Background(), cfg, src, "demo"); err == nil {
		t.Fatal("expected ownership failure")
	}
}
func TestPlanEngineRequiresEndpoint(t *testing.T) {
	if _, err := core.OpenPlanEngine(context.Background(), "docker", ""); err == nil {
		t.Fatal("expected explicit endpoint requirement")
	}
}
