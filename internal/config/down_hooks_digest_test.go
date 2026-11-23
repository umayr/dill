package config

import "testing"

func TestDownHookDigest(t *testing.T) {
	cfg := &DillConfig{Services: map[string]*Service{"web": {Hooks: &LifecycleHooks{BeforeDown: []Hook{{Job: "backup"}}}}}, Jobs: map[string]*Job{"backup": {Image: "backup", AfterJobs: []string{"prepare"}}, "prepare": {Image: "prepare"}}}
	a, err := DownHookDigest(cfg, "web")
	if err != nil || a == "" {
		t.Fatalf("digest: %q %v", a, err)
	}
	cfg.Jobs["prepare"].Image = "changed"
	b, err := DownHookDigest(cfg, "web")
	if err != nil || a == b {
		t.Fatalf("transitive change not committed: %v", err)
	}
	delete(cfg.Jobs, "prepare")
	if _, err := DownHookDigest(cfg, "web"); err == nil {
		t.Fatal("missing job accepted")
	}
	cfg.Jobs["prepare"] = &Job{AfterJobs: []string{"backup"}}
	if _, err := DownHookDigest(cfg, "web"); err == nil {
		t.Fatal("cycle accepted")
	}
}
