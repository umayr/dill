package orchestrator

import (
	"context"
	"errors"
	"io"

	"github.com/umayr/dill/internal/config"
)

// ErrNotFound is returned by InspectConfig when the named container does not exist.
var ErrNotFound = errors.New("container not found")

// Dill attaches the following labels to every container it manages.
// These are stable API — external tools (e.g. UIs) may rely on them.
//
//	dill.managed=true          present on every dill-managed container
//	dill.stack=<stackName>     associates the container with a named stack
//	dill.service=<name>        logical service name within the stack
//	dill.config-hash=<sha256>  desired service configuration fingerprint

// LiveConfig holds the effective configuration of a running or stopped container,
// extracted from the engine for comparison against desired state.
type LiveConfig struct {
	Image         string
	Env           map[string]string // KEY → value; only user-set vars
	Ports         []config.PortBinding
	Volumes       []config.VolumeMount
	RestartPolicy string
	UserLabels    map[string]string // excludes dill.* system labels
	SystemLabels  map[string]string // dill.* system labels
	NetworkMode   string
	User          string
	HealthTest    []string
	Command       []string
	CapAdd        []string
	Init          bool
	Cpuset        string
}

// ContainerStatus holds runtime information about a running or stopped container.
type ContainerStatus struct {
	Name   string   `json:"name"`   // container name (without leading /)
	ID     string   `json:"id"`     // short ID (12 chars)
	State  string   `json:"state"`  // "running", "exited", "paused", ...
	Status string   `json:"status"` // human-readable, e.g. "Up 2 hours"
	Image  string   `json:"image"`  // image name
	Ports  []string `json:"ports"`  // e.g. ["0.0.0.0:8080->80/tcp"]
}

// Engine defines the methods required by Dill to manage containers.
type Engine interface {
	// PullImage pulls the named image from its registry, writing download
	// progress to w. Implementations write one line per progress update.
	PullImage(ctx context.Context, image string, w io.Writer) error

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

	// InspectConfig returns the live configuration of the named container.
	// Returns ErrNotFound (unwrappable via errors.Is) when the container does not exist.
	InspectConfig(ctx context.Context, name string) (*LiveConfig, error)

	// ListStack returns the names of all containers bearing both
	// dill.managed=true and dill.stack=stackName labels.
	ListStack(ctx context.Context, stackName string) ([]string, error)

	// EnsureNetwork creates a bridge network with the given name if it does
	// not already exist. Idempotent.
	EnsureNetwork(ctx context.Context, name string) error

	// RemoveNetwork removes the named network. A no-op if it does not exist.
	RemoveNetwork(ctx context.Context, name string) error

	// RemoveImage removes a local image by name or ID.
	// If force is true the image is removed even if containers reference it.
	RemoveImage(ctx context.Context, image string, force bool) error

	// RemoveVolume removes a named volume.
	// If force is true the volume is removed even if in use.
	RemoveVolume(ctx context.Context, name string, force bool) error

	// Close releases any resources held by the engine client.
	Close() error
}
