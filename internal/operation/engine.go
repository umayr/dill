package operation

import (
	"context"
	"errors"
	"io"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// Engine preserves the underlying adapter's behavior. StartService can create
// networks/volumes/containers internally even when it returns an empty ID and
// an error. Its resource entry therefore describes an aggregate create scope,
// not a complete inventory; any failing call has unknown effects.
type Engine struct {
	orchestrator.Engine
	Recorder *Recorder
	Adapter  string
	Borrowed bool // invocation owner releases the underlying engine after all handlers
}

func (e *Engine) Close() error {
	if e.Borrowed {
		return nil
	}
	return e.Engine.Close()
}

func (e *Engine) CLIEndpoint() string {
	if target, ok := e.Engine.(interface{ CLIEndpoint() string }); ok {
		return target.CLIEndpoint()
	}
	return ""
}

// EngineName avoids concrete-type inference when an engine is decorated.
func (e *Engine) EngineName() string {
	if e.Adapter != "" {
		return e.Adapter
	}
	if named, ok := e.Engine.(interface{ EngineName() string }); ok {
		return named.EngineName()
	}
	if _, ok := e.Engine.(*orchestrator.DockerEngine); ok {
		return "docker"
	}
	if _, ok := e.Engine.(*orchestrator.PodmanEngine); ok {
		return "podman"
	}
	return ""
}

func (e *Engine) PullImage(ctx context.Context, image string, _ io.Writer) error {
	done := e.Recorder.Begin("image", image, "pull")
	err := e.Engine.PullImage(ctx, image, io.Discard)
	done("", err)
	return err
}
func (e *Engine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	done := e.Recorder.Begin("createScope", name, "create")
	id, err := e.Engine.StartService(ctx, name, svc, stack)
	done(id, err)
	return id, err
}
func (e *Engine) StopService(ctx context.Context, id string) error {
	done := e.Recorder.Begin("container", id, "stop")
	err := e.Engine.StopService(ctx, id)
	done("", err)
	return err
}
func (e *Engine) StartExisting(ctx context.Context, id string) error {
	done := e.Recorder.Begin("container", id, "start")
	err := e.Engine.StartExisting(ctx, id)
	done("", err)
	return err
}
func (e *Engine) RemoveService(ctx context.Context, id string) error {
	done := e.Recorder.Begin("container", id, "remove")
	err := e.Engine.RemoveService(ctx, id)
	done("", err)
	return err
}
func (e *Engine) EnsureNetwork(ctx context.Context, name string) error {
	done := e.Recorder.Begin("network", name, "ensure")
	err := e.Engine.EnsureNetwork(ctx, name)
	done("", err)
	return err
}
func (e *Engine) EnsureVolume(ctx context.Context, name, stack string) error {
	done := e.Recorder.Begin("volume", name, "ensure")
	err := e.Engine.EnsureVolume(ctx, name, stack)
	done("", err)
	return err
}
func (e *Engine) RemoveImage(ctx context.Context, id string, force bool) error {
	done := e.Recorder.Begin("image", id, "remove")
	err := e.Engine.RemoveImage(ctx, id, force)
	done("", err)
	return err
}

// EnforceDisposableVolumes preserves an explicit optional capability through
// this decorator. Lack of support is an error, never a silent opt-out.
func (e *Engine) EnforceDisposableVolumes() error {
	if enforcer, ok := e.Engine.(interface{ EnforceDisposableVolumes() error }); ok {
		return enforcer.EnforceDisposableVolumes()
	}
	return errors.New("disposable volume enforcement unavailable")
}

// DisposableImage forwards prepared local immutable identity and metadata.
// Missing support is unavailable, never permission to pull, ignore image VOLUME
// entries, or substitute a mutable tag for the attested local image ID.
func (e *Engine) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	inspector, ok := e.Engine.(interface {
		DisposableImage(context.Context, string) (string, []string, error)
	})
	if !ok {
		return "", nil, errors.New("disposable image inspection unavailable")
	}
	return inspector.DisposableImage(ctx, image)
}

func (e *Engine) PreviewResources(ctx context.Context, stack string, names []string) ([]orchestrator.PreviewResource, error) {
	inspector, ok := e.Engine.(orchestrator.PreviewResourceInspector)
	if !ok {
		return nil, errors.New("resource inspection unavailable")
	}
	return inspector.PreviewResources(ctx, stack, names)
}

// RemoveNetwork and RemoveVolume deliberately retain their delegated read-only
// retention checks. Neither adapter implements deletion of these resources.
