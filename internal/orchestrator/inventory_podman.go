package orchestrator

import (
	"context"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"strings"
)

func (p *PodmanEngine) inventoryRecords(ctx context.Context, stack string) ([]inventoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	all := true
	labels := []string{"dill.managed=true"}
	if stack != "" {
		labels = append(labels, "dill.stack="+stack)
	}
	cs, err := containers.List(inventoryPodmanContext{Context: ctx, connection: p.conn}, &containers.ListOptions{All: &all, Filters: map[string][]string{"label": labels}})
	if err != nil {
		return nil, inventoryError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(cs) > MaxInventoryRecords {
		return nil, ErrInventoryTooLarge
	}
	out := make([]inventoryRecord, 0, len(cs))
	for _, c := range cs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, inventoryRecord{id: c.ID, name: name, service: c.Labels["dill.service"], stack: c.Labels["dill.stack"], state: c.State, managed: c.Labels["dill.managed"] == "true"})
	}
	return out, nil
}
func (p *PodmanEngine) InventoryStacks(ctx context.Context) ([]InventoryStack, error) {
	records, err := p.inventoryRecords(ctx, "")
	if err != nil {
		return nil, err
	}
	return inventoryStacks(ctx, records)
}
func (p *PodmanEngine) InventoryContainers(ctx context.Context, stack string) ([]InventoryContainer, error) {
	if stack == "" {
		return nil, ErrInventoryStackRequired
	}
	records, err := p.inventoryRecords(ctx, stack)
	if err != nil {
		return nil, err
	}
	return inventoryContainers(ctx, records, stack)
}

// inventoryPodmanContext only exposes engine-owned connection values.
type inventoryPodmanContext struct {
	context.Context
	connection context.Context
}

func (c inventoryPodmanContext) Value(key any) any { return c.connection.Value(key) }
