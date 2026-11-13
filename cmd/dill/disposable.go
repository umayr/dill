package main

import (
	"fmt"

	"github.com/umayr/dill/internal/config"
)

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
