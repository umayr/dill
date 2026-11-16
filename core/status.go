// Package core exposes reusable, process-independent Dill operations.
//
// Status, ordinary planning and deployment, and stack-wide down are supported.
// Callers are responsible for stack selection, endpoint policy, known-secret
// downgrade guards and operation coordination; file-secret operations are not exposed.
package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/umayr/dill/internal/orchestrator"
)

// ContainerStatus is the engine's description of a container.
type ContainerStatus = orchestrator.ContainerStatus

// StatusSource is the read-only engine capability needed by Status.
type StatusSource interface {
	ListStack(context.Context, string) ([]string, error)
	ServiceStatus(context.Context, string) (*ContainerStatus, error)
}

// StatusResult is a snapshot of the containers belonging to a stack.
type StatusResult struct {
	Stack      string             `json:"stack"`
	Engine     string             `json:"engine"`
	ConfigFile string             `json:"config_file"`
	Services   int                `json:"services"`
	Containers []*ContainerStatus `json:"containers"`
}

// Status lists a stack and inspects each container. As with the CLI, an
// individual inspection failure is represented in the result rather than
// aborting the whole snapshot, except for context cancellation or deadline.
// A listing failure aborts the operation.
func Status(ctx context.Context, source StatusSource, stack, engine, configFile string, services int) (StatusResult, error) {
	if err := ctx.Err(); err != nil {
		return StatusResult{}, err
	}
	names, err := source.ListStack(ctx, stack)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return StatusResult{}, ctxErr
	}
	if err != nil {
		return StatusResult{}, fmt.Errorf("listing stack: %w", err)
	}
	out := StatusResult{Stack: stack, Engine: engine, ConfigFile: configFile, Services: services, Containers: []*ContainerStatus{}}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return StatusResult{}, err
		}
		cs, err := source.ServiceStatus(ctx, name)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return StatusResult{}, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return StatusResult{}, err
		}
		if err != nil {
			cs = &ContainerStatus{Name: name, State: "error", Status: err.Error()}
		}
		out.Containers = append(out.Containers, cs)
	}
	if err := ctx.Err(); err != nil {
		return StatusResult{}, err
	}
	return out, nil
}

// StatusEngine is a read-only status source that owns its engine connection.
type StatusEngine interface {
	StatusSource
	Close() error
}

// OpenStatusEngine opens a Docker or Podman connection at an explicit endpoint.
// Endpoint authorization and validation are the caller's responsibility.
// It never selects an endpoint from process environment on behalf of callers.
func OpenStatusEngine(ctx context.Context, engine, endpoint string) (StatusEngine, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("status engine endpoint is required")
	}
	switch engine {
	case "docker":
		return orchestrator.NewDockerEngineAt(endpoint)
	case "podman":
		return orchestrator.NewPodmanEngine(ctx, endpoint)
	default:
		return nil, fmt.Errorf("unknown engine %q", engine)
	}
}
