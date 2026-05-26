package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/plan"
)

func runPlan(ctx context.Context, configFile string, args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	sn := resolveStackName(cfg, configFile)
	result, err := plan.Compute(ctx, cfg, engine, sn)
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	switch *format {
	case "json":
		if err := plan.RenderJSON(result, os.Stdout); err != nil {
			return fmt.Errorf("rendering plan: %w", err)
		}
	default:
		plan.Render(result, os.Stdout)
	}

	for _, ch := range result.Changes {
		if ch.Kind != plan.KindNoop {
			return errPlanHasChanges
		}
	}
	return nil
}

// errPlanHasChanges is returned by runPlan when drift is detected.
// The caller exits with code 1 without printing an error message —
// the rendered plan output already communicates the situation.
var errPlanHasChanges = errors.New("plan has changes")
