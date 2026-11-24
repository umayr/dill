package core

import (
	"context"
	"fmt"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// PrepareDownHooks verifies the reviewed definitions against the immutable
// service identities captured for removal before executing any beforeDown job.
// The continuation must only be invoked after successful container removal.
func PrepareDownHooks(ctx context.Context, e DownEngine, intent *orchestrator.RemovalIntent, cfg *Config) (func() error, error) {
	var before, after []string
	for _, r := range intent.Resources {
		if intent.DownHookDigests[r.ID] == "" && (cfg == nil || cfg.Services[r.Service] == nil || cfg.Services[r.Service].Hooks == nil) {
			continue
		}
		live, err := e.InspectConfig(ctx, r.Name)
		if err != nil {
			return nil, err
		}
		if err = orchestrator.CheckOwnership(r.Name, intent.Stack, r.Service, live); err != nil {
			return nil, err
		}
		if live.ID != r.ID {
			return nil, fmt.Errorf("down target %q changed", r.Name)
		}
		want := ""
		if cfg != nil && cfg.Services[r.Service] != nil {
			want, err = config.DownHookDigest(cfg, r.Service)
			if err != nil {
				return nil, err
			}
		}
		if want != live.SystemLabels[config.DownHookLabel] {
			return nil, fmt.Errorf("down hook definition unavailable or changed for %q; supply applied project", r.Service)
		}
		if want != "" {
			h := cfg.Services[r.Service].Hooks
			before = append(before, config.MatchedJobs(h, "beforeDown", "")...)
			after = append(after, config.MatchedJobs(h, "afterDown", "")...)
		}
	}
	if len(before)+len(after) == 0 {
		return nil, nil
	}
	eng, ok := e.(orchestrator.Engine)
	if !ok {
		return nil, fmt.Errorf("down job engine unavailable")
	}
	if err := RunHookJobs(ctx, eng, intent.Stack, cfg, before); err != nil {
		return nil, err
	}
	return func() error {
		if err := RunHookJobs(ctx, eng, intent.Stack, cfg, after); err != nil {
			return fmt.Errorf("removed; afterDown failed: %w", err)
		}
		return nil
	}, nil
}
