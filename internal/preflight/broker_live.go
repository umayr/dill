package preflight

import (
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// BrokerContainerDigest retains normal ownership/complete-fact checks and
// admits protected facts only with fresh private target-generation evidence.
func BrokerContainerDigest(stack, service, requested string, live *orchestrator.LiveConfig, cfg *config.DillConfig, m *brokersecret.Materialized) (string, error) {
	if m == nil || stack != m.Scope().Stack {
		return "", brokersecret.ErrUnavailable
	}
	if _, _, err := m.References(cfg, false); err != nil {
		return "", err
	}
	if err := orchestrator.CheckBrokerMounts(service, live, m); err != nil {
		return "", err
	}
	return containerDigest(stack, service, requested, live, true)
}
