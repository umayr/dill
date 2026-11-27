package orchestrator

import (
	"context"
	"errors"
	"io"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
)

var ErrReadOnlyEngine = errors.New("engine mutation unavailable during read-only capture")

// DenyMutations is a read-only CAPTURE guard, not a dry-run reconciler. Every
// engine mutator fails BEFORE delegation, even idempotent ensures/retained
// deletes. Read metadata and transport Close retain their actual semantics.
// Keep its unit classification exhaustive when the Engine interface changes.
func DenyMutations(engine Engine) Engine { return &readOnlyEngine{Engine: engine} }

type readOnlyEngine struct{ Engine }

func (e *readOnlyEngine) EnforceBrokerResources(cfg *config.DillConfig, m *brokersecret.Materialized) error {
	if p, ok := e.Engine.(BrokerResourceEnforcer); ok {
		return p.EnforceBrokerResources(cfg, m)
	}
	return brokersecret.ErrUnsupported
}
func (*readOnlyEngine) StartServiceBroker(context.Context, string, *config.Service, string, *config.DillConfig, *brokersecret.Materialized) (string, error) {
	return "", ErrReadOnlyEngine
}

func (*readOnlyEngine) PullImage(context.Context, string, io.Writer) error { return ErrReadOnlyEngine }
func (*readOnlyEngine) StartService(context.Context, string, *config.Service, string) (string, error) {
	return "", ErrReadOnlyEngine
}
func (*readOnlyEngine) StopService(context.Context, string) error          { return ErrReadOnlyEngine }
func (*readOnlyEngine) StartExisting(context.Context, string) error        { return ErrReadOnlyEngine }
func (*readOnlyEngine) RemoveService(context.Context, string) error        { return ErrReadOnlyEngine }
func (*readOnlyEngine) EnsureNetwork(context.Context, string) error        { return ErrReadOnlyEngine }
func (*readOnlyEngine) RemoveNetwork(context.Context, string) error        { return ErrReadOnlyEngine }
func (*readOnlyEngine) RemoveImage(context.Context, string, bool) error    { return ErrReadOnlyEngine }
func (*readOnlyEngine) EnsureVolume(context.Context, string, string) error { return ErrReadOnlyEngine }
func (*readOnlyEngine) RemoveVolume(context.Context, string, string, bool) error {
	return ErrReadOnlyEngine
}
func (e *readOnlyEngine) EngineName() string {
	if p, ok := e.Engine.(interface{ EngineName() string }); ok {
		return p.EngineName()
	}
	return ""
}
func (e *readOnlyEngine) CLIEndpoint() string {
	if p, ok := e.Engine.(interface{ CLIEndpoint() string }); ok {
		return p.CLIEndpoint()
	}
	return ""
}
func (e *readOnlyEngine) EnforceDisposableVolumes() error {
	if p, ok := e.Engine.(DisposableVolumeEnforcer); ok {
		return p.EnforceDisposableVolumes()
	}
	return ErrDisposableUnsupported
}

// Installing a restriction is local metadata, not permission to mutate.
func (e *readOnlyEngine) EnforcePreparedImages(images map[string]PreparedImage) error {
	if p, ok := e.Engine.(PreparedImageEnforcer); ok {
		return p.EnforcePreparedImages(images)
	}
	return ErrDisposableUnavailable
}
func (e *readOnlyEngine) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	if p, ok := e.Engine.(DisposableImageInspector); ok {
		return p.DisposableImage(ctx, image)
	}
	return "", nil, ErrDisposableUnsupported
}
func (e *readOnlyEngine) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	if p, ok := e.Engine.(PreviewResourceInspector); ok {
		return p.PreviewResources(ctx, stack, names)
	}
	return nil, ErrDisposableUnsupported
}
