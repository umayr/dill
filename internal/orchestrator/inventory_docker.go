package orchestrator

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
)

func (d *DockerEngine) inventoryRecords(ctx context.Context, stack string) ([]inventoryRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f := filters.NewArgs(filters.Arg("label", "dill.managed=true"))
	if stack != "" {
		f.Add("label", "dill.stack="+stack)
	}
	cs, err := d.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
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
func (d *DockerEngine) InventoryStacks(ctx context.Context) ([]InventoryStack, error) {
	records, err := d.inventoryRecords(ctx, "")
	if err != nil {
		return nil, err
	}
	return inventoryStacks(ctx, records)
}
func (d *DockerEngine) InventoryContainers(ctx context.Context, stack string) ([]InventoryContainer, error) {
	if stack == "" {
		return nil, ErrInventoryStackRequired
	}
	records, err := d.inventoryRecords(ctx, stack)
	if err != nil {
		return nil, err
	}
	return inventoryContainers(ctx, records, stack)
}
