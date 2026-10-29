package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/umayr/dill/internal/plan"
)

func runPlan(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "plan")
}

func runDiff(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "diff")
}

func runPlanNamed(ctx context.Context, opts commandOptions, args []string, name string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), name); err != nil {
		return err
	}

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	sn := resolveStackNameWithOptions(cfg, opts)
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
