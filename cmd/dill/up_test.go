package main

import (
	"context"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/plan"
)

type noopPullSink struct{}

func (noopPullSink) Write(p []byte) (int, error) { return len(p), nil }
func (noopPullSink) Begin()                      {}
func (noopPullSink) Done()                       {}

func TestPullAlwaysImages(t *testing.T) {
	fake := newFakeEngine()
	services := map[string]*config.Service{
		"always":  {Image: "always:1", PullPolicy: "always"},
		"missing": {Image: "missing:1", PullPolicy: "missing"},
		"never":   {Image: "never:1", PullPolicy: "never"},
	}

	err := pullAlwaysImages(context.Background(), fake, services, func(string) dag.PullSink {
		return noopPullSink{}
	})
	if err != nil {
		t.Fatalf("pullAlwaysImages: %v", err)
	}
	if len(fake.pulled) != 1 || fake.pulled[0] != "always:1" {
		t.Fatalf("pulled = %v, want [always:1]", fake.pulled)
	}
	if services["always"].PullPolicy != "missing" {
		t.Errorf("always policy = %q, want missing after pre-pull", services["always"].PullPolicy)
	}
	if services["missing"].PullPolicy != "missing" || services["never"].PullPolicy != "never" {
		t.Errorf("policies changed unexpectedly: %+v", services)
	}
}

func TestFilterPlanSubset(t *testing.T) {
	p := &plan.Plan{Changes: []plan.Change{
		{Service: "app", Kind: plan.KindCreate},
		{Service: "dep", Kind: plan.KindNoop},
		{Service: "other", Kind: plan.KindCreate},
		{Service: "orphan-container", Kind: plan.KindRemove},
	}}
	selected := map[string]*config.Service{"app": {Image: "x"}}

	got := filterPlan(p, selected, false)
	if len(got.Changes) != 1 || got.Changes[0].Service != "app" {
		t.Fatalf("filterPlan = %+v, want only app", got.Changes)
	}
}

func TestFilterPlanFullKeepsOrphans(t *testing.T) {
	changes := []plan.Change{
		{Service: "app", Kind: plan.KindCreate},
		{Service: "other", Kind: plan.KindNoop},
		{Service: "orphan-container", Kind: plan.KindRemove},
	}
	selected := map[string]*config.Service{
		"app":   {Image: "x"},
		"other": {Image: "x"},
	}

	got := filterPlan(&plan.Plan{Changes: changes}, selected, true)
	if len(got.Changes) != 3 {
		t.Fatalf("filterPlan = %+v, want all 3 changes", got.Changes)
	}
}

func TestWithoutDependencies(t *testing.T) {
	services := map[string]*config.Service{
		"app": {Image: "x", DependsOn: []string{"dep"}},
	}
	got := withoutDependencies(services)
	if len(got["app"].DependsOn) != 0 {
		t.Fatalf("copy still has dependencies: %+v", got["app"].DependsOn)
	}
	if len(services["app"].DependsOn) != 1 {
		t.Fatalf("original was mutated: %+v", services["app"].DependsOn)
	}
}
