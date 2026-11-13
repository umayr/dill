package dag

import (
	"context"
	"errors"
	"time"

	"github.com/umayr/dill/internal/orchestrator"
)

// ErrCapturedSelectionChanged means the runner refused a missing/replaced
// compared entry. It does NOT imply engineEffects none: a late recheck can run
// after ensures/another service. The operation recorder retains those facts.
var ErrCapturedSelectionChanged = errors.New("captured resource selection changed")

// RunCaptured executes the SAME dependency runner with an exhaustive mapping of
// service key to compared immutable container ID. Empty ID is checked absence,
// not permission to discover a substitute. No entry may be missing or extra.
func (g *Graph) RunCaptured(ctx context.Context, engine orchestrator.Engine, stackName string, timeout time.Duration, actions map[string]Action, prog Progress, mkSink MakePullSink, capturedIDs map[string]string) error {
	if capturedIDs == nil || len(actions) != len(g.nodes) {
		return ErrCapturedSelectionChanged
	}
	ids := make(map[string]string, len(capturedIDs))
	checkedActions := make(map[string]Action, len(actions))
	for name, id := range capturedIDs {
		action, ok := actions[name]
		if !ok || action < ActionCreate || action > ActionStart || ((action == ActionCreate) != (id == "")) {
			return ErrCapturedSelectionChanged
		}
		ids[name] = id
		checkedActions[name] = action
	}
	return g.run(ctx, engine, stackName, timeout, checkedActions, prog, mkSink, ids)
}
func (g *Graph) validateCapturedIDs(ctx context.Context, engine orchestrator.Engine, stack string, ids map[string]string) error {
	if len(ids) != len(g.nodes) {
		return ErrCapturedSelectionChanged
	}
	for _, n := range g.nodes {
		expected, ok := ids[n.name]
		if !ok {
			return ErrCapturedSelectionChanged
		}
		name := n.svc.ContainerName
		if name == "" {
			name = stack + "_" + n.name
		}
		live, err := engine.InspectConfig(ctx, name)
		if expected == "" {
			if !errors.Is(err, orchestrator.ErrNotFound) {
				return ErrCapturedSelectionChanged
			}
			continue
		}
		if err != nil || live == nil || live.ID != expected {
			return ErrCapturedSelectionChanged
		}
		if err = orchestrator.CheckOwnership(name, stack, n.name, live); err != nil {
			return ErrCapturedSelectionChanged
		}
	}
	return nil
}
