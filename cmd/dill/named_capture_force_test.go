package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// Force processing belongs to the authoritative capture producer. A handler
// must not independently upgrade a reviewed action after comparison, even if
// handed inconsistent transient state by an internal programming mistake.
func TestNamedUpNeverUpgradesReviewedActionsAfterComparison(t *testing.T) {
	cfg, e, opts := namedHandlerFixture("up")
	opts.ForceRecreate = true
	err := runUpConfig(context.Background(), opts, cfg, nil, false, "", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { return e, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.mutations, []string{"network", "start:reviewed-id"}) {
		t.Fatal("reviewed action independently upgraded", e.mutations)
	}
}
