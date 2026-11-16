package core

import (
	"context"
	"fmt"
	"reflect"

	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
)

// DownEngine is the explicit engine capability needed for stack-wide cleanup.
// ListStack must return only names bearing this stack's managed labels. InspectConfig
// must return authoritative IDs and labels; mutation methods must accept IDs.
// RemoveNetwork must fail safely if it cannot prove network removal is safe.
type DownEngine interface {
	ListStack(context.Context, string) ([]string, error)
	InspectConfig(context.Context, string) (*LiveConfig, error)
	StopService(context.Context, string) error
	RemoveService(context.Context, string) error
	RemoveNetwork(context.Context, string) error
}

// DownEvent records an attempted phase. A nil error does not prove deletion:
// the resource may already be absent, or a network may have been retained.
type DownEvent struct {
	Phase, Name string
	Err         error
}

// DownResult records attempts even when Down returns an error. It is not a
// transactional receipt or proof of the final engine state.
type DownResult struct{ Events []DownEvent }

// Down removes stack containers using label ownership and pinned identities.
// It captures all identities before mutation, continues independent cleanup on
// failure, and returns joined errors with partial attempts in the result. It
// does not remove volumes or images, load configuration, or deliver secrets.
// Callers are responsible for endpoint authorization and coordination with
// other operations. This API has no access to Brine known-secret records or
// proof and coordination policy. It must not be used to bypass external
// known-secret downgrade guards, checked or qualified operations, or any
// authorization requirement. Service integration is not yet authorized.
// Progress, if non-nil, runs synchronously before removal.
func Down(ctx context.Context, engine DownEngine, stack string, progress func(string, string)) (DownResult, error) {
	var result DownResult
	if ctx == nil {
		return result, fmt.Errorf("context is required")
	}
	if engine == nil {
		return result, fmt.Errorf("down engine is required")
	}
	v := reflect.ValueOf(engine)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if v.IsNil() {
			return result, fmt.Errorf("down engine is required")
		}
	}
	if stack == "" {
		return result, fmt.Errorf("stack name is required")
	}
	err := dag.DownWithEvents(ctx, engine, stack, progress, func(phase, name string, err error) {
		result.Events = append(result.Events, DownEvent{Phase: phase, Name: name, Err: err})
	})
	return result, err
}

// DownConnection is an explicitly opened engine connection owned by its caller.
type DownConnection interface {
	DownEngine
	Close() error
}

// OpenDownEngine opens a Docker or Podman endpoint explicitly selected and
// authorized by the caller. No process environment is consulted for selection.
func OpenDownEngine(ctx context.Context, engine, endpoint string) (DownConnection, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("down engine endpoint is required")
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
