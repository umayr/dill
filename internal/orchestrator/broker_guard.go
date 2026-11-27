package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"io"
	"sort"
)

type BrokerResourceEnforcer interface {
	EnforceBrokerResources(*config.DillConfig, *brokersecret.Materialized) error
}

func validateBrokerService(s *config.Service) error {
	if s == nil || s.Privileged || len(s.Entrypoint) > 0 || len(s.DeclaredVolumes) > 0 || len(s.Conditions) > 0 || s.Hooks != nil || s.PullPolicy != "never" || s.NetworkMode != "" || len(s.Devices) > 0 || len(s.CapAdd) > 0 {
		return brokersecret.ErrUnsupported
	}
	mounts, err := s.NormalizedVolumes()
	if err != nil {
		return brokersecret.ErrUnavailable
	}
	for _, m := range mounts {
		if m.Type != "volume" || m.External || m.Source == "" || m.Name != "" {
			return brokersecret.ErrUnsupported
		}
	}
	return nil
}
func ValidateBrokerConfig(cfg *config.DillConfig, m *brokersecret.Materialized) error {
	if cfg == nil || m == nil || len(cfg.Jobs) != 0 || len(cfg.Volumes) != 0 {
		return brokersecret.ErrUnsupported
	}
	if _, _, err := m.References(cfg, false); err != nil {
		return err
	}
	for _, svc := range cfg.Services {
		if err := validateBrokerService(svc); err != nil {
			return err
		}
	}
	return nil
}
func PreflightBrokerResources(ctx context.Context, e Engine, cfg *config.DillConfig, stack string, m *brokersecret.Materialized) error {
	if ctx == nil || m == nil || stack != m.Scope().Stack || ValidateBrokerConfig(cfg, m) != nil {
		return brokersecret.ErrUnavailable
	}
	if verifier, ok := e.(interface {
		VerifyBrokerRuntime(*brokersecret.Materialized) error
	}); ok {
		if err := verifier.VerifyBrokerRuntime(m); err != nil {
			return err
		}
	}
	if err := CheckBrokerInventory(ctx, e, cfg, stack, m); err != nil {
		return err
	}
	set := map[string]bool{}
	for _, svc := range cfg.Services {
		mounts, err := svc.NormalizedVolumes()
		if err != nil {
			return brokersecret.ErrUnavailable
		}
		for _, mount := range mounts {
			set[mount.EngineName(stack)] = true
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	if err := preflightOwnedResources(ctx, e, cfg, stack, names); err != nil {
		return brokersecret.ErrUnavailable
	}
	return nil
}

// This constructor accepts only an opaque qualified Docker connection. The
// physical endpoint commitment is separate from the SDK's fixed HTTP origin.
func NewBrokerDockerEngine(target *coordination.Target, cfg *config.DillConfig, m *brokersecret.Materialized) (*DockerEngine, error) {
	if target == nil || target.Engine() != "docker" || (!target.BrokerRootful() && target.NamespaceDigest() == "") || target.Client() == nil || m == nil || cfg == nil || "sha256:"+target.Identity() != m.Scope().TargetDigest || target.EndpointDigest() == "" || target.EndpointDigest() != m.Scope().EndpointDigest {
		return nil, brokersecret.ErrUnavailable
	}
	if err := ValidateBrokerConfig(cfg, m); err != nil {
		return nil, err
	}
	inspectedClient, err := brokerDockerInspectClient(target.Client())
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	d, err := NewDockerEngineWithClient(coordination.Endpoint, inspectedClient)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	d.brokerEndpoint = target.EndpointDigest()
	d.brokerTarget = target
	if err = d.VerifyBrokerRuntime(m); err != nil {
		return nil, err
	}
	return d, nil
}
func (d *DockerEngine) BrokerEndpointDigest() string {
	if d == nil {
		return ""
	}
	return d.brokerEndpoint
}
func (d *DockerEngine) EnforceBrokerResources(cfg *config.DillConfig, m *brokersecret.Materialized) error {
	if d == nil || d.cli == nil || d.brokerEndpoint == "" || !brokerEndpointMatches(d, cfg, m) || ValidateBrokerConfig(cfg, m) != nil || d.disposable {
		return brokersecret.ErrUnavailable
	}
	d.broker = true
	return nil
}

// Only the qualified constructor may enable broker mutation. An ordinary SDK
// connection never becomes authority merely by matching socket spelling.
func (p *PodmanEngine) EnforceBrokerResources(cfg *config.DillConfig, m *brokersecret.Materialized) error {
	if p == nil || p.qualifiedBroker == nil || p.disposable || p.brokerEndpoint == "" || !brokerEndpointMatches(p, cfg, m) || ValidateBrokerConfig(cfg, m) != nil {
		return brokersecret.ErrUnsupported
	}
	if err := p.VerifyBrokerRuntime(m); err != nil {
		return err
	}
	p.broker = true
	return nil
}

type brokerGuard struct {
	Engine
	cfg      *config.DillConfig
	stack    string
	material *brokersecret.Materialized
}

func WithBrokerGuardrails(ctx context.Context, e Engine, cfg *config.DillConfig, stack string, m *brokersecret.Materialized) (Engine, error) {
	if err := PreflightBrokerResources(ctx, e, cfg, stack, m); err != nil {
		return nil, err
	}
	enforcer, ok := e.(BrokerResourceEnforcer)
	if !ok {
		return nil, brokersecret.ErrUnsupported
	}
	if err := enforcer.EnforceBrokerResources(cfg, m); err != nil {
		return nil, err
	}
	return &brokerGuard{e, cfg, stack, m}, nil
}
func (e *brokerGuard) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	if stack != e.stack || e.cfg.Services[name] == nil || validateBrokerService(svc) != nil {
		return "", brokersecret.ErrUnavailable
	}
	if err := PreflightBrokerResources(ctx, e.Engine, e.cfg, stack, e.material); err != nil {
		return "", err
	}
	creator, ok := e.Engine.(BrokerCreator)
	if !ok {
		return "", brokersecret.ErrUnsupported
	}
	return creator.StartServiceBroker(ctx, name, svc, stack, e.cfg, e.material)
}
func (e *brokerGuard) EnsureNetwork(ctx context.Context, stack string) error {
	if stack != e.stack {
		return brokersecret.ErrUnavailable
	}
	if err := PreflightBrokerResources(ctx, e.Engine, e.cfg, stack, e.material); err != nil {
		return err
	}
	return e.Engine.EnsureNetwork(ctx, stack)
}
func (e *brokerGuard) PullImage(context.Context, string, io.Writer) error {
	return brokersecret.ErrUnsupported
}
func (e *brokerGuard) EnforcePreparedImages(images map[string]PreparedImage) error {
	if len(images) != len(e.cfg.Services) {
		return brokersecret.ErrUnavailable
	}
	for name, svc := range e.cfg.Services {
		if image, ok := images[name]; !ok || image.Reference != svc.Image {
			return brokersecret.ErrUnavailable
		}
	}
	if p, ok := e.Engine.(PreparedImageEnforcer); ok {
		return p.EnforcePreparedImages(images)
	}
	return brokersecret.ErrUnsupported
}
func (e *brokerGuard) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	return e.Engine.(DisposableImageInspector).DisposableImage(ctx, image)
}
func (e *brokerGuard) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	return e.Engine.(PreviewResourceInspector).PreviewResources(ctx, stack, names)
}
func (e *brokerGuard) EngineName() string  { return e.cfg.Engine }
func (e *brokerGuard) CLIEndpoint() string { return "" } // no subprocess escape
