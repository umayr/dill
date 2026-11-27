package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"reflect"
)

const (
	BrokerRegistrationLabel = "dill.broker-registration"
	BrokerGenerationLabel   = "dill.broker-generation"
	BrokerHandleLabel       = "dill.broker-handle"
)

type brokerCreateKey struct{}

func applyBrokerLabels(ctx context.Context, labels map[string]string) {
	if review, ok := ctx.Value(brokerCreateKey{}).(brokersecret.Review); ok {
		labels[SecretEnabledLabel] = "true"
		labels[BrokerRegistrationLabel] = review.Scope.Registration
		labels[BrokerGenerationLabel] = review.Scope.Generation
		labels[BrokerHandleLabel] = review.Handle
	}
}

// BrokerCreator is deliberately separate from retired CheckedSecretCreator.
// Only an active, opaque target materialization can supply creation inputs.
type BrokerCreator interface {
	StartServiceBroker(context.Context, string, *config.Service, string, *config.DillConfig, *brokersecret.Materialized) (string, error)
}
type brokerEngine struct {
	Engine
	cfg      *config.DillConfig
	material *brokersecret.Materialized
}

func WithBrokerSecrets(e Engine, cfg *config.DillConfig, m *brokersecret.Materialized) (Engine, error) {
	if e == nil || reflect.ValueOf(e).Kind() == reflect.Ptr && reflect.ValueOf(e).IsNil() || m == nil || cfg == nil {
		return nil, brokersecret.ErrUnavailable
	}
	if _, ok := e.(BrokerCreator); !ok {
		return nil, brokersecret.ErrUnsupported
	}
	if _, _, err := m.References(cfg, true); err != nil {
		return nil, err
	}
	if !brokerEndpointMatches(e, cfg, m) {
		return nil, brokersecret.ErrUnavailable
	}
	return &brokerEngine{e, cfg, m}, nil
}
func (e *brokerEngine) CLIEndpoint() string {
	return e.Engine.(interface{ CLIEndpoint() string }).CLIEndpoint()
}
func (e *brokerEngine) EngineName() string { return e.cfg.Engine }
func (e *brokerEngine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	return e.Engine.(BrokerCreator).StartServiceBroker(ctx, name, svc, stack, e.cfg, e.material)
}
func brokerEndpointMatches(e any, cfg *config.DillConfig, m *brokersecret.Materialized) bool {
	target, ok := e.(interface {
		CLIEndpoint() string
		EngineName() string
	})
	if !ok || cfg == nil || m == nil {
		return false
	}
	scope := m.Scope()
	endpoint := brokersecret.Digest([]byte(target.CLIEndpoint()))
	if qualified, ok := e.(interface{ BrokerEndpointDigest() string }); ok && qualified.BrokerEndpointDigest() != "" {
		endpoint = qualified.BrokerEndpointDigest()
	}
	return scope.Engine == cfg.Engine && target.EngineName() == cfg.Engine && target.CLIEndpoint() != "" && endpoint == scope.EndpointDigest
}
func brokerMounts(ctx context.Context, name string, svc *config.Service, stack string, e any, cfg *config.DillConfig, m *brokersecret.Materialized) ([]config.ResolvedSecret, string, error) {
	if ctx == nil || ctx.Err() != nil || cfg == nil || svc == nil || m == nil || stack != m.Scope().Stack || !brokerEndpointMatches(e, cfg, m) {
		return nil, "", brokersecret.ErrUnavailable
	}
	expected := cfg.Services[name]
	if expected == nil || expected.BaseDir != svc.BaseDir || !reflect.DeepEqual(expected.DeclaredVolumes, svc.DeclaredVolumes) {
		return nil, "", brokersecret.ErrUnavailable
	}
	want, err := json.Marshal(expected)
	if err != nil {
		return nil, "", brokersecret.ErrUnavailable
	}
	actual, err := json.Marshal(svc)
	if err != nil || !bytes.Equal(want, actual) {
		return nil, "", brokersecret.ErrUnavailable
	}
	if svc.DownHookDigest != "" {
		digest, err := config.DownHookDigest(cfg, name)
		if err != nil || digest != svc.DownHookDigest {
			return nil, "", brokersecret.ErrUnavailable
		}
	}
	refs, target, err := m.References(cfg, true)
	if err != nil {
		return nil, "", err
	}
	return refs[name], target, nil
}
func (d *DockerEngine) StartServiceBroker(ctx context.Context, name string, svc *config.Service, stack string, cfg *config.DillConfig, m *brokersecret.Materialized) (id string, err error) {
	if d == nil || d.cli == nil || !d.broker {
		return "", brokersecret.ErrUnavailable
	}
	defer func() {
		if err != nil {
			err = brokersecret.ErrUnavailable
		}
	}()
	if err = d.VerifyBrokerRuntime(m); err != nil {
		return "", err
	}
	mounts, target, err := brokerMounts(ctx, name, svc, stack, d, cfg, m)
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, brokerCreateKey{}, m.Review())
	return d.startService(ctx, name, svc, stack, mounts, target)
}
func (p *PodmanEngine) StartServiceBroker(ctx context.Context, name string, svc *config.Service, stack string, cfg *config.DillConfig, m *brokersecret.Materialized) (id string, err error) {
	if p == nil || p.socket == "" || !p.broker {
		return "", brokersecret.ErrUnavailable
	}
	defer func() {
		if err != nil {
			err = brokersecret.ErrUnavailable
		}
	}()
	if err = p.VerifyBrokerRuntime(m); err != nil {
		return "", err
	}
	mounts, target, err := brokerMounts(ctx, name, svc, stack, p, cfg, m)
	if err != nil {
		return "", err
	}
	ctx = context.WithValue(ctx, brokerCreateKey{}, m.Review())
	return p.startService(ctx, name, svc, stack, mounts, target)
}
