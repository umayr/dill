//go:build linux

package brokersecret

import (
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func assertLiveReviewDenied(t *testing.T, m *Materialized, handle, generation, service string) {
	t.Helper()
	if err := m.AcceptLive(handle, generation); err != ErrUnavailable {
		t.Errorf("AcceptLive: got %v, want ErrUnavailable", err)
	}
	if mounts, err := m.LiveMounts(handle, generation, service); mounts != nil || err != ErrUnavailable {
		t.Errorf("LiveMounts: returned mounts = %t, error = %v; want nil mounts and ErrUnavailable", mounts != nil, err)
	}
}

func TestLiveReviewRejectsNilMaterializationAndStore(t *testing.T) {
	id := strings.Repeat("a", 32)
	assertLiveReviewDenied(t, nil, id, id, "web")
	assertLiveReviewDenied(t, &Materialized{}, id, id, "web")
}

func TestLiveReviewAuthenticatesGenerationAndRefreshesMounts(t *testing.T) {
	s, e, grants, _ := runtimeFixture(t)
	defer e.Close()
	r, err := s.Prepare(e, e.Scope, grants)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Resolve(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	generation, service := e.Scope.Generation, grants[0].Service
	assertLiveReviewDenied(t, pending, r.Handle, generation, service)
	active, err := s.Activate(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, handle, generation string }{
		{"invalid handle", "invalid", generation},
		{"invalid generation", r.Handle, "invalid"},
		{"wrong generation", r.Handle, strings.Repeat("d", 32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertLiveReviewDenied(t, active, tc.handle, tc.generation, service)
		})
	}
	if mounts, err := active.LiveMounts(r.Handle, generation, "invalid/service"); mounts != nil || err != ErrUnavailable {
		t.Fatal("invalid service did not fail closed", err)
	}
	if err := active.AcceptLive(r.Handle, generation); err != nil {
		t.Fatal(err)
	}
	mounts, err := active.LiveMounts(r.Handle, generation, service)
	if err != nil || len(mounts) == 0 {
		t.Fatal("active service mounts unavailable", err)
	}
	if err := os.Remove(mounts[0].HostPath); err != nil {
		t.Fatal(err)
	}
	if mounts, err := active.LiveMounts(r.Handle, generation, service); mounts != nil || err == nil {
		t.Fatal("LiveMounts accepted missing mount evidence")
	}
}

func TestLiveReviewRetirementOnlyAllowsListedPredecessor(t *testing.T) {
	s, e, grants, _ := runtimeFixture(t)
	defer e.Close()
	old, err := s.Prepare(e, e.Scope, grants)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activate(old.Handle, e.Scope); err != nil {
		t.Fatal(err)
	}
	otherEnvelope := e
	otherEnvelope.Scope.Generation = strings.Repeat("d", 32)
	otherEnvelope.Scope.BindingRevision++
	other, err := s.Prepare(otherEnvelope, otherEnvelope.Scope, grants)
	if err != nil {
		t.Fatal(err)
	}
	otherActive, err := s.Activate(other.Handle, other.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := otherActive.AcceptLive(other.Handle, other.Scope.Generation); err != nil {
		t.Fatal("unlisted generation is not active", err)
	}
	next := other.Scope
	next.Generation = strings.Repeat("f", 32)
	next.BindingRevision++
	cfg := &config.DillConfig{Name: next.Stack, Engine: next.Engine, Services: map[string]*config.Service{"web": {Image: "alpine"}}}
	desc, err := Describe(cfg, next.Stack)
	if err != nil {
		t.Fatal(err)
	}
	next.ConfigDigest = desc.ConfigDigest
	r, err := s.PrepareRetirement(Retirement{
		Contract: RetirementVersion,
		Scope:    next,
		Previous: []Reference{{Scope: old.Scope, Handle: old.Handle}},
	})
	if err != nil {
		t.Fatal(err)
	}
	retirement, err := s.Resolve(r.Handle, next)
	if err != nil {
		t.Fatal(err)
	}
	service := grants[0].Service
	if err := retirement.AcceptLive(old.Handle, old.Scope.Generation); err != nil {
		t.Fatal("listed predecessor rejected", err)
	}
	if mounts, err := retirement.LiveMounts(old.Handle, old.Scope.Generation, service); err != nil || len(mounts) == 0 {
		t.Fatal("listed predecessor mounts unavailable", err)
	}
	assertLiveReviewDenied(t, retirement, other.Handle, other.Scope.Generation, service)
}
