package orchestrator

import (
	"context"
	"errors"
	"sort"
)

// Inventory is the complete read-only surface for discovering Dill containers.
// Results are point-in-time snapshots and may change immediately after a call.
// Only containers carrying dill.managed=true and a non-empty dill.stack qualify.
// Labels can be spoofed and are not proof of ownership or authorization.
// At most MaxInventoryRecords engine records are processed; larger responses fail
// without partial results. SDKs materialize responses before this limit applies.
type Inventory interface {
	InventoryStacks(context.Context) ([]InventoryStack, error)
	InventoryContainers(context.Context, string) ([]InventoryContainer, error)
}

// InventoryStack identifies a stack represented by at least one container.
type InventoryStack struct{ Name string }

// InventoryContainer contains only identifying labels and runtime state, not
// configuration, environment, image references, ports, or engine diagnostics.
type InventoryContainer struct{ ID, Name, Service, State string }

const MaxInventoryRecords = 10000

var ErrInventoryTooLarge = errors.New("container inventory exceeds record limit")
var ErrInventoryUnavailable = errors.New("container inventory unavailable")
var ErrInventoryStackRequired = errors.New("inventory stack name required")

type inventoryRecord struct {
	id, name, service, stack, state string
	managed                         bool
}

func inventoryError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return ErrInventoryUnavailable
}

func inventoryStacks(ctx context.Context, records []inventoryRecord) ([]InventoryStack, error) {
	names := map[string]bool{}
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.managed && r.stack != "" {
			names[r.stack] = true
		}
	}
	out := make([]InventoryStack, 0, len(names))
	for name := range names {
		out = append(out, InventoryStack{Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func inventoryContainers(ctx context.Context, records []inventoryRecord, stack string) ([]InventoryContainer, error) {
	out := make([]InventoryContainer, 0)
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.managed || r.stack != stack {
			continue
		}
		state := r.state
		switch state {
		case "created", "running", "paused", "restarting", "removing", "exited", "dead", "stopped":
		default:
			state = "unknown"
		}
		out = append(out, InventoryContainer{ID: r.id, Name: r.name, Service: r.service, State: state})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
