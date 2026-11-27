//go:build linux

package brokersecret

import (
	"github.com/umayr/dill/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetirementStoreAuthenticatesPreviousAndRetainsFiles(t *testing.T) {
	s, e, grants, root := runtimeFixture(t)
	defer e.Close()
	old, err := s.Prepare(e, e.Scope, grants)
	if err != nil {
		t.Fatal(err)
	}
	next := e.Scope
	next.Generation = strings.Repeat("f", 32)
	cfg := &config.DillConfig{Name: e.Scope.Stack, Engine: e.Scope.Engine, Services: map[string]*config.Service{"web": {Image: "alpine"}}}
	desc, err := Describe(cfg, e.Scope.Stack)
	if err != nil {
		t.Fatal(err)
	}
	next.ConfigDigest = desc.ConfigDigest
	next.BindingRevision++
	r := Retirement{Contract: RetirementVersion, Scope: next, Previous: []Reference{{Scope: e.Scope, Handle: old.Handle}}}
	if _, err = s.PrepareRetirement(r); err == nil {
		t.Fatal("inactive predecessor authorized retirement")
	}
	prior, err := s.Activate(old.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	file := prior.Mounts()[0].HostPath
	prepared, err := s.PrepareRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.Resolve(prepared.Handle, next)
	if err != nil || !empty.IsRetirement() || len(empty.Mounts()) != 0 || len(empty.Previous()) != 1 {
		t.Fatal("empty metadata not authenticated", err)
	}
	if refs, _, err := empty.References(cfg, false); err != nil || len(refs) != 0 {
		t.Fatal("explicit retirement config cannot be referenced", err)
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("retirement removed historical file")
	}
	// Unauthenticated retirement metadata cannot be altered without detection.
	manifest := filepath.Join(root, prepared.Handle, "manifest.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(raw), old.Handle, strings.Repeat("e", 32), 1)
	if err = os.WriteFile(manifest, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(prepared.Handle, next); err == nil {
		t.Fatal("tampered prior reference accepted")
	}
}
