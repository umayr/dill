package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

// labelStack is the minimum context needed to operate on a stack from its
// container labels when the Pkl config cannot be loaded.
type labelStack struct {
	Config *config.DillConfig
	Name   string
	Engine string
}

// resolveLabelStack loads the config when possible and falls back to
// -p/--stack-name + --engine when it cannot be parsed. Without an explicit
// stack name there is no way to identify the stack, so the load error is
// returned with a hint.
func resolveLabelStack(ctx context.Context, opts commandOptions) (*labelStack, error) {
	// A launcher may intentionally provide an invocation-private nonexistent
	// absolute recipe for label-only cleanup. Never invoke Pkl for this narrow
	// fully explicit identity. Existing entries (including dangling symlinks)
	// and errors other than ENOENT retain the ordinary resolution path.
	if opts.ConfigFileSet && filepath.IsAbs(opts.ConfigFile) && opts.StackNameSet && config.ValidateSecretStackName(opts.StackName) == nil && opts.Engine != "" && opts.EngineSocketSet {
		if _, err := os.Lstat(opts.ConfigFile); errors.Is(err, syscall.ENOENT) {
			if opts.Engine != "docker" && opts.Engine != "podman" {
				return nil, usageError{"invalid explicit cleanup engine"}
			}
			if _, err := localEngineSocket(opts); err != nil {
				return nil, err
			}
			return &labelStack{Name: opts.StackName, Engine: opts.Engine}, nil
		}
	}

	cfg, err := loadConfig(ctx, opts)
	if err == nil {
		return &labelStack{
			Config: cfg,
			Name:   resolveStackNameWithOptions(cfg, opts),
			Engine: cfg.Engine,
		}, nil
	}
	if opts.StackName == "" {
		return nil, fmt.Errorf("%w\nhint: pass -p/--stack-name (and --engine if needed) to operate from container labels", err)
	}
	if opts.Engine == "" {
		logger.Warn("config unavailable; assuming podman", "hint", "pass --engine docker if the stack uses docker")
	}
	logger.Debug("config unavailable; operating from container labels", "err", err)
	return &labelStack{Name: opts.StackName, Engine: opts.Engine}, nil
}

// requireConfig rejects commands that need service-to-container mapping when
// the config could not be loaded.
func (s *labelStack) requireConfig(command string) error {
	if s.Config == nil {
		return usageError{fmt.Sprintf("%s with service names requires a valid config file", command)}
	}
	return nil
}
