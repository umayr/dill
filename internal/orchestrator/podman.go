package orchestrator

import (
	"context"
	"fmt"

	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/umayr/dill/internal/config"
)

type PodmanEngine struct {
	conn context.Context
}

func NewPodmanEngine(ctx context.Context, socket string) (*PodmanEngine, error) {
	conn, err := bindings.NewConnection(ctx, socket)
	if err != nil {
		return nil, err
	}
	return &PodmanEngine{conn: conn}, nil
}

func (p *PodmanEngine) StartService(ctx context.Context, name string, svc *config.Service) error {
	fmt.Printf("[Podman] Starting %s...\n", name)
	s := specgen.NewSpecGenerator(svc.Image, false)
	if svc.ContainerName != "" {
		s.Name = svc.ContainerName
	}
	s.Labels = map[string]string{"dill.managed": "true", "dill.service": name}

	res, err := containers.CreateWithSpec(p.conn, s, nil)
	if err != nil {
		return err
	}
	return containers.Start(p.conn, res.ID, nil)
}

func (p *PodmanEngine) RemoveService(ctx context.Context, name string) error {
	return nil // Implementation for teardown
}

func (p *PodmanEngine) Close() error { return nil }
