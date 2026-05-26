package config

import (
	"fmt"
	"strings"
)

const SystemLabelPrefix = "dill."

// ValidateRuntime checks invariants that Pkl's type system cannot express.
// These checks protect Dill's ownership labels and catch shorthand values that
// are parsed after Pkl evaluation.
func ValidateRuntime(cfg *DillConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	for name, svc := range cfg.Services {
		if svc == nil {
			return fmt.Errorf("service %q is nil", name)
		}
		for label := range svc.Labels {
			if strings.HasPrefix(label, SystemLabelPrefix) {
				return fmt.Errorf("service %q label %q uses reserved prefix %q", name, label, SystemLabelPrefix)
			}
		}
		if _, err := NormalizePorts(svc.Ports); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if _, err := NormalizeVolumes(svc.Volumes, svc.BaseDir); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if svc.HealthCheck != nil {
			if _, err := NormalizeHealthCheckTest(svc.HealthCheck.Test); err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
		}
	}
	return nil
}
