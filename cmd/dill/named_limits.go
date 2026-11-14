package main

import (
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/preflight"
)

// namedDeclaredLimitExceeded recognizes count/display budget failures before
// state access. Other invalid config is left to the normal profile validation.
func namedDeclaredLimitExceeded(cfg *config.DillConfig, stack string) bool {
	if cfg == nil {
		return false
	}
	if len(stack) > preflight.MaxNameBytes {
		return true
	}
	volumes := map[string]bool{}
	for service, svc := range cfg.Services {
		if svc == nil {
			continue
		}
		if len(serviceContainerName(stack, service, svc)) > preflight.MaxNameBytes {
			return true
		}
		mounts, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			continue
		}
		for _, mount := range mounts {
			if mount.Type != "volume" {
				continue
			}
			name := mount.EngineName(stack)
			if len(name) > preflight.MaxNameBytes {
				return true
			}
			volumes[name] = true
		}
	}
	return 1+len(cfg.Services)+len(volumes) > preflight.MaxResources
}
