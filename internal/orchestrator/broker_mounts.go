package orchestrator

import (
	"github.com/umayr/dill/internal/brokersecret"
)

// CheckBrokerMounts rejects arbitrary bind mounts hidden behind broker labels.
// Ordinary named-volume membership remains a separate whole-inventory check.
func CheckBrokerMounts(service string, live *LiveConfig, m *brokersecret.Materialized) error {
	if err := CheckBrokerLive(live, m); err != nil {
		return err
	}
	if _, protected := live.SystemLabels[SecretEnabledLabel]; !protected {
		for _, mount := range live.Volumes {
			if mount.Type == "bind" {
				return brokersecret.ErrUnavailable
			}
		}
		return nil
	}
	mounts, err := m.LiveMounts(live.SystemLabels[BrokerHandleLabel], live.SystemLabels[BrokerGenerationLabel], service)
	if err != nil {
		return err
	}
	expected := map[string]brokersecret.Mount{}
	for _, mount := range mounts {
		expected[mount.Target] = mount
	}
	for _, actual := range live.Volumes {
		if actual.Type != "bind" {
			continue
		}
		want, ok := expected[actual.Target]
		if !ok || want.HostPath != actual.Source || !actual.ReadOnly {
			return brokersecret.ErrUnavailable
		}
		delete(expected, actual.Target)
	}
	if !live.MountsKnown || len(expected) != 0 {
		return brokersecret.ErrUnavailable
	}
	return nil
}
