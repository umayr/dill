package orchestrator

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/umayr/dill/internal/config"
)

type DockerEngine struct {
	cli *client.Client
}

func NewDockerEngine() (*DockerEngine, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerEngine{cli: cli}, nil
}

func (d *DockerEngine) StartService(ctx context.Context, name string, svc *config.Service) error {
	fmt.Printf("[Docker] Starting %s...\n", name)

	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image:  svc.Image,
		Labels: map[string]string{"dill.managed": "true", "dill.service": name},
	}, nil, nil, nil, svc.ContainerName)
	if err != nil {
		return err
	}

	return d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
}

func (d *DockerEngine) RemoveService(ctx context.Context, name string) error {
	return nil // Implementation for teardown
}

func (d *DockerEngine) Close() error { return d.cli.Close() }
