package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/umayr/dill/internal/config"
)

var (
	ErrDisposableUnsupported = errors.New("unsupported capability in the disposable-volume Alpha profile")
	ErrDisposableUnavailable = errors.New("disposable Alpha resource preflight unavailable")
	ErrDisposableOwnership   = errors.New("disposable Alpha resource is not dedicated to this stack")
)

// ValidateDisposableConfig is deliberately narrower than normal Dill support.
// No bind path qualification, host/namespace sharing, devices, added capabilities,
// external/anonymous/explicit-name volumes, or file secrets are supported here.
// Selecting this profile attests that all stack data and credentials are synthetic
// and disposable; it cannot infer whether ordinary environment values are secrets.
func ValidateDisposableConfig(cfg *config.DillConfig) error {
	if cfg == nil || len(cfg.Secrets) > 0 {
		return ErrDisposableUnsupported
	}
	for _, svc := range cfg.Services {
		if svc == nil || len(svc.Secrets) > 0 || svc.NetworkMode != "" || len(svc.Devices) > 0 || len(svc.CapAdd) > 0 {
			return ErrDisposableUnsupported
		}
		mounts, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			return ErrDisposableUnsupported
		}
		for _, m := range mounts {
			if m.Type != "volume" || m.External || m.Source == "" || m.Name != "" {
				return ErrDisposableUnsupported
			}
		}
	}
	return nil
}

func disposableVolumes(cfg *config.DillConfig, stack string) ([]string, error) {
	if err := ValidateDisposableConfig(cfg); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, svc := range cfg.Services {
		mounts, _ := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		for _, m := range mounts {
			names[m.EngineName(stack)] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

type disposableSettings struct {
	Driver          string
	Options, Labels map[string]string
}

func checkDisposableSettings(stack, driver string, settings disposableSettings) error {
	if stack == "" || settings.Labels["dill.managed"] != "true" || settings.Labels["dill.stack"] != stack {
		return ErrDisposableOwnership
	}
	if settings.Driver != driver || len(settings.Options) != 0 {
		return ErrDisposableUnsupported
	}
	return nil
}

// PreflightDisposableResources reads adapter evidence only. It never creates a
// network, volume or image. Missing or malformed evidence fails closed. A local
// volume driver without options is a bounded check, not NAS/mount identity proof.
func PreflightDisposableResources(ctx context.Context, engine Engine, cfg *config.DillConfig, stack string) error {
	names, err := disposableVolumes(cfg, stack)
	if err != nil {
		return err
	}
	inspector, ok := engine.(PreviewResourceInspector)
	if !ok || stack == "" {
		return ErrDisposableUnavailable
	}
	facts, err := inspector.PreviewResources(ctx, stack, names)
	if err != nil {
		return ErrDisposableUnavailable
	}
	want := map[string]bool{"network\x00" + stack: true}
	for _, name := range names {
		want["volume\x00"+name] = true
	}
	for _, fact := range facts {
		key := fact.Kind + "\x00" + fact.Name
		if !want[key] {
			return ErrDisposableUnavailable
		}
		delete(want, key)
		if !fact.Exists {
			continue
		}
		var settings disposableSettings
		if json.Unmarshal(fact.Settings, &settings) != nil {
			return ErrDisposableUnavailable
		}
		driver := "local"
		if fact.Kind == "network" {
			driver = "bridge"
			if fact.ID == "" {
				return ErrDisposableUnavailable
			}
		}
		if err := checkDisposableSettings(stack, driver, settings); err != nil {
			return err
		}
	}
	if len(want) > 0 {
		return ErrDisposableUnavailable
	}
	return ctx.Err()
}

type disposableEngine struct {
	Engine
	cfg   *config.DillConfig
	stack string
}

// DisposableVolumeEnforcer must be explicitly forwarded by engine decorators.
// Returning success promises that every adapter EnsureVolume call revalidates
// ownership/driver/options, including responses to create requests.
type DisposableVolumeEnforcer interface {
	EnforceDisposableVolumes() error
}

// WithDisposableGuardrails performs all-resource preflight before returning a
// mutation-capable engine, then revalidates before network/container creation.
// Adapters additionally check volume ownership in EnsureVolume, including
// create responses, rather than silently adopting an existing volume by name.
func WithDisposableGuardrails(ctx context.Context, engine Engine, cfg *config.DillConfig, stack string) (Engine, error) {
	if err := PreflightDisposableResources(ctx, engine, cfg, stack); err != nil {
		return nil, err
	}
	enforcer, ok := engine.(DisposableVolumeEnforcer)
	if !ok {
		return nil, ErrDisposableUnavailable
	}
	if err := enforcer.EnforceDisposableVolumes(); err != nil {
		return nil, ErrDisposableUnavailable
	}
	return &disposableEngine{Engine: engine, cfg: cfg, stack: stack}, nil
}

func (e *disposableEngine) EnsureNetwork(ctx context.Context, stack string) error {
	if stack != e.stack {
		return ErrDisposableOwnership
	}
	if err := PreflightDisposableResources(ctx, e.Engine, e.cfg, stack); err != nil {
		return err
	}
	return e.Engine.EnsureNetwork(ctx, stack)
}

func (e *disposableEngine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	if stack != e.stack || e.cfg.Services[name] == nil {
		return "", ErrDisposableOwnership
	}
	// --no-deps supplies service copies, so pointer identity is not authority.
	// Validate the actual creation intent as well as the original full config.
	candidate := &config.DillConfig{Services: map[string]*config.Service{name: svc}}
	if err := PreflightDisposableResources(ctx, e.Engine, candidate, stack); err != nil {
		return "", err
	}
	if err := PreflightDisposableResources(ctx, e.Engine, e.cfg, stack); err != nil {
		return "", err
	}
	return e.Engine.StartService(ctx, name, svc, stack)
}

// Preserve read-only evidence and endpoint delegation through the wrapper.
func (e *disposableEngine) EngineName() string {
	if named, ok := e.Engine.(interface{ EngineName() string }); ok {
		return named.EngineName()
	}
	return ""
}
func (e *disposableEngine) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	return e.Engine.(PreviewResourceInspector).PreviewResources(ctx, stack, names)
}
func (e *disposableEngine) CLIEndpoint() string {
	if endpoint, ok := e.Engine.(interface{ CLIEndpoint() string }); ok {
		return endpoint.CLIEndpoint()
	}
	return ""
}
