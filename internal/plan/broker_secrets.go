package plan

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"reflect"
)

// ComputeBroker uses the existing authoritative service diff and orphan checks.
// It is not engine qualification or authorization. Callers independently hold
// the source/target/ownership coordination lease and bind the returned plan to
// the target preparation, config digest and exact selection before mutation.
func ComputeBroker(ctx context.Context, cfg *config.DillConfig, e Source, stack string, m *brokersecret.Materialized) (*Plan, error) {
	if ctx == nil || ctx.Err() != nil || cfg == nil || e == nil || reflect.ValueOf(e).Kind() == reflect.Ptr && reflect.ValueOf(e).IsNil() || m == nil || stack != m.Scope().Stack {
		return nil, brokersecret.ErrUnavailable
	}
	endpoint, ok := e.(interface {
		CLIEndpoint() string
		EngineName() string
	})
	if !ok || endpoint.EngineName() != cfg.Engine || m.Scope().Engine != cfg.Engine || brokersecret.Digest([]byte(endpoint.CLIEndpoint())) != m.Scope().EndpointDigest {
		return nil, brokersecret.ErrUnavailable
	}
	if orchestrator.CheckBrokerInventory(ctx, e, cfg, stack, m) != nil {
		return nil, brokersecret.ErrUnavailable
	}
	mounts, target, err := m.References(cfg, false)
	if err != nil {
		return nil, err
	}
	// Check every known desired workload before reusing protected live facts.
	for name, svc := range cfg.Services {
		container := svc.ContainerName
		if container == "" {
			container = stack + "_" + name
		}
		live, inspectErr := e.InspectConfig(ctx, container)
		if isNotFound(inspectErr) {
			continue
		}
		if inspectErr != nil || orchestrator.CheckBrokerMounts(name, live, m) != nil {
			return nil, brokersecret.ErrUnavailable
		}
	}
	p, err := computeMode(ctx, cfg, e, stack, nil, mounts, target, true)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	review := m.Review()
	for i := range p.Changes {
		ch := &p.Changes[i]
		if ch.Kind != KindNoop {
			continue
		}
		svc := cfg.Services[ch.Service]
		name := svc.ContainerName
		if name == "" {
			name = stack + "_" + ch.Service
		}
		live, err := e.InspectConfig(ctx, name)
		if err != nil || live == nil {
			return nil, brokersecret.ErrUnavailable
		}
		if live.SystemLabels[orchestrator.BrokerHandleLabel] != review.Handle || live.SystemLabels[orchestrator.BrokerGenerationLabel] != review.Scope.Generation {
			ch.Kind = KindRecreate
			ch.Diffs = append(ch.Diffs, FieldDiff{Field: "secret_generation", Before: "previous", After: "reviewed generation"})
		}
	}
	return p, nil
}
