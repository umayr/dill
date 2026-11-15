package main

import (
	"errors"

	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

var errNamedCaptureUnavailable = errors.New("checked resource capture unavailable")

// namedCapture is PRIVATE transient state from ONE fresh qualified comparison.
// It is never decoded from caller JSON or loaded as a saved-plan authorization.
// Only the checked adapter may mark compared after matching the fresh digest.
// Existing handlers consume this capture; later checks can refuse replacements,
// never reselect targets. Nil preserves the ordinary unbound implementation.
type namedCapture struct {
	operation, stack string
	compared         bool
	upPlan           *plan.Plan
	serviceIDs       map[string]string // exhaustive service keys; empty ID is proven absence
	stopIDs          map[string]string // present names only, compared immutable IDs
	removal          *orchestrator.RemovalIntent
}

func (o commandOptions) checkedCapture(operation, stack string) (*namedCapture, error) {
	if o.operation == nil || o.operation.named == nil {
		return nil, nil
	}
	c := o.operation.named
	if !c.compared || c.operation != operation || c.stack != stack {
		return nil, errNamedCaptureUnavailable
	}
	return c, nil
}
func cloneNamedPlan(p *plan.Plan) *plan.Plan {
	if p == nil {
		return nil
	}
	out := &plan.Plan{Changes: make([]plan.Change, len(p.Changes))}
	for i, ch := range p.Changes {
		out.Changes[i] = ch
		out.Changes[i].Diffs = append([]plan.FieldDiff{}, ch.Diffs...)
	}
	return out
}
