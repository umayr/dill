package main

import (
	"context"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
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
