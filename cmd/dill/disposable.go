package main

import (
	"fmt"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// effectiveDisposableConfig validates actual selected pull policies, including
// --pull never and --no-deps copies, without changing unselected services. It
// still qualifies every declared mount/capability/image before writes.
func effectiveDisposableConfig(cfg *config.DillConfig, selected map[string]*config.Service) (*config.DillConfig, error) {
	copy := *cfg
	copy.Services = make(map[string]*config.Service, len(cfg.Services))
	for name, svc := range cfg.Services {
		service := *svc
		service.PullPolicy = "never" // unselected services cannot pull
		if effective, ok := selected[name]; ok {
			service = *effective
		}
		copy.Services[name] = &service
	}
	if err := orchestrator.ValidateDisposableConfig(&copy); err != nil {
		return nil, err
	}
	return &copy, nil
}

// The pilot requires an explicit operator-selected local target, not ambient
// Docker configuration. This is not proof of daemon identity or isolation.
func validateDisposableTarget(opts commandOptions, cfg *config.DillConfig) error {
	if opts.Engine != "docker" || cfg.Engine != "docker" || !opts.EngineSocketSet || (!opts.StackNameSet && opts.StackName == "") || opts.StackName == "" {
		return fmt.Errorf("Alpha disposable profile requires explicit --engine docker, --engine-socket and --stack-name")
	}
	socket, err := localEngineSocket(opts)
	if err != nil || socket == "" {
		return fmt.Errorf("Alpha disposable profile requires an explicit local engine socket")
	}
	if opts.secretMode() {
		return fmt.Errorf("secret bindings are unsupported in the disposable-volume Alpha profile")
	}
	return nil
}
