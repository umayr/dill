package orchestrator

import (
	"context"
	"io"

	"github.com/umayr/dill/internal/config"
)

// ContainerStatus holds runtime information about a running or stopped container.
type ContainerStatus struct {
	Name   string   // container name (without leading /)
	ID     string   // short ID (12 chars)
	State  string   // "running", "exited", "paused", ...
	Status string   // human-readable, e.g. "Up 2 hours" or "Exited (0) 3 minutes ago"
	Image  string   // image name
	Ports  []string // e.g. ["0.0.0.0:8080->80/tcp"]
}

// Engine defines the methods required by Dill to manage containers.
type Engine interface {
	// PullImage pulls the named image from its registry.
	PullImage(ctx context.Context, image string) error

	// ImageExists reports whether the image is already present locally.
	ImageExists(ctx context.Context, image string) (bool, error)

	// StartService creates and starts the container for svc.
	// stackName is used as the dill.stack label value for later teardown scoping.
	// Returns the container ID on success.
	StartService(ctx context.Context, name string, svc *config.Service, stackName string) (string, error)

	// StopService stops the running container identified by name.
	StopService(ctx context.Context, name string) error

	// StartExisting starts a previously stopped container by name.
	StartExisting(ctx context.Context, name string) error

	// RemoveService removes the stopped container identified by name.
	RemoveService(ctx context.Context, name string) error

	// IsReady returns true when the container is in a usable state.
	// If hasHealthCheck is true, the container must reach "healthy" status;
	// otherwise running state is sufficient.
	IsReady(ctx context.Context, name string, hasHealthCheck bool) (bool, error)

	// ServiceStatus returns runtime status information for the named container.
	ServiceStatus(ctx context.Context, name string) (*ContainerStatus, error)

	// Logs streams container output to w. If follow is true, it blocks until
	// the context is cancelled. tail limits output to the last N lines (0 = all).
	Logs(ctx context.Context, name string, follow bool, tail int, w io.Writer) error

	// ListStack returns the names of all containers bearing both
	// dill.managed=true and dill.stack=stackName labels.
	ListStack(ctx context.Context, stackName string) ([]string, error)

	// Close releases any resources held by the engine client.
	Close() error
}
