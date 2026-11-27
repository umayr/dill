package orchestrator

import (
	"github.com/umayr/dill/internal/brokersecret"
)

// CheckBrokerLive refuses adoption of retired strict-secret workloads. A label
// is not evidence: every broker-marked generation must still be authenticated
// and active in this target's private generation store.
func CheckBrokerLive(live *LiveConfig, m *brokersecret.Materialized) error {
	if live == nil || m == nil {
		return brokersecret.ErrUnavailable
	}
	_, protected := live.SystemLabels[SecretEnabledLabel]
	_, registration := live.SystemLabels[BrokerRegistrationLabel]
	_, generation := live.SystemLabels[BrokerGenerationLabel]
	_, handle := live.SystemLabels[BrokerHandleLabel]
	if !protected && !registration && !generation && !handle {
		return nil
	}
	if !protected || live.SystemLabels[SecretEnabledLabel] != "true" || !registration || !generation || !handle || live.SystemLabels[BrokerRegistrationLabel] != m.Scope().Registration {
		return brokersecret.ErrUnavailable
	}
	return m.AcceptLive(live.SystemLabels[BrokerHandleLabel], live.SystemLabels[BrokerGenerationLabel])
}
