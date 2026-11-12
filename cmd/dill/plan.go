package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/plan"
)

func runPlan(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "plan")
}

func runDiff(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "diff")
}

func runPlanNamed(ctx context.Context, opts commandOptions, args []string, name string) (retErr error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	// Once JSON mode is selected, failures before rendering still produce one
	// JSON document. Successful plan output and drift exits remain unchanged.
	defer func() {
		if *format == "json" && retErr != nil && !errors.Is(retErr, errPlanHasChanges) {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"error": retErr.Error(), "kind": failureKind(retErr)}); err != nil {
				retErr = fmt.Errorf("rendering failure: %w", err)
			}
		}
	}()
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

	if err := config.RejectUnsupportedSecretDelivery(cfg); err != nil {
		return err
	}
	engine, err := openEngine(ctx, cfg, opts)
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

func failureKind(err error) string {
	var usage usageError
	if errors.As(err, &usage) {
		return "invalid_input"
	}
	var evaluator *config.EvaluatorError
	if errors.As(err, &evaluator) {
		return "evaluator_failure"
	}
	var invalid *config.InvalidError
	if errors.As(err, &invalid) {
		return "invalid_input"
	}
	return "operational_failure"
}

// errPlanHasChanges is returned by runPlan when drift is detected.
// The caller exits with code 1 without printing an error message —
// the rendered plan output already communicates the situation.
var errPlanHasChanges = errors.New("plan has changes")
