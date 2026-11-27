package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
)

type brokerInventorySource interface {
	ListStack(context.Context, string) ([]string, error)
	InspectConfig(context.Context, string) (*LiveConfig, error)
}

// CheckBrokerInventory requires exhaustive stack membership. No broker mode
// prunes omitted workloads. Every retained live workload has ownership and exact
// authenticated mounts checked; retirement also restricts predecessor handles.
func CheckBrokerInventory(ctx context.Context, e brokerInventorySource, cfg *config.DillConfig, stack string, m *brokersecret.Materialized) error {
	if ctx == nil || ctx.Err() != nil || e == nil || cfg == nil || m == nil || !m.Scope().Valid() || m.Scope().Stack != stack {
		return brokersecret.ErrUnavailable
	}
	listed, err := e.ListStack(ctx, stack)
	if err != nil {
		return brokersecret.ErrUnavailable
	}
	desired, err := brokerMembership(cfg, stack, listed)
	if err != nil {
		return err
	}
	for _, name := range listed {
		service := desired[name]
		live, err := e.InspectConfig(ctx, name)
		if err != nil || !BrokerDefaultNamespace(live) || CheckOwnership(name, stack, service, live) != nil || CheckBrokerMounts(service, live, m) != nil {
			return brokersecret.ErrUnavailable
		}
	}
	return nil
}

// Membership is syntax/coverage only, never evidence of ownership or MAC validity.
func brokerMembership(cfg *config.DillConfig, stack string, listed []string) (map[string]string, error) {
	desired := map[string]string{}
	for service, svc := range cfg.Services {
		if svc == nil {
			return nil, brokersecret.ErrUnavailable
		}
		name := svc.ContainerName
		if name == "" {
			name = stack + "_" + service
		}
		if _, dup := desired[name]; dup {
			return nil, brokersecret.ErrUnavailable
		}
		desired[name] = service
	}
	seen := map[string]bool{}
	for _, name := range listed {
		service, ok := desired[name]
		if !ok || seen[name] {
			return nil, brokersecret.ErrUnavailable
		}
		_ = service
		seen[name] = true
	}
	return desired, nil
}
