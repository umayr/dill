package orchestrator

import (
	"context"

	"github.com/umayr/dill/internal/config"
)

// Engine defines the methods required by Dill to manage containers.
type Engine interface {
	StartService(ctx context.Context, name string, svc *config.Service) error
	RemoveService(ctx context.Context, name string) error
	Close() error
}
