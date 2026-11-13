package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/secretfile"
)

func runPlan(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "plan")
}

func runDiff(ctx context.Context, opts commandOptions, args []string) error {
	return runPlanNamed(ctx, opts, args, "diff")
}

func runPlanNamed(ctx context.Context, opts commandOptions, args []string, name string) (retErr error) {
	opts, restore := secretCommandOptions(opts, args)
	defer restore()

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	if name == "plan" {
		addProofFlags(fs, &opts)
	}
	format := fs.String("format", "text", "output format: text or json")
	// Once JSON mode is selected, failures before rendering still produce one
	// JSON document. Successful plan output and drift exits remain unchanged.
	defer func() {
		if opts.secretMode() {
			retErr = fixedSecretError(retErr)
		}
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

	if opts.proofRequested() && (name != "plan" || !opts.ProofKeyFileSet || !opts.ProofFileSet || !filepath.IsAbs(opts.ProofKeyFile) || !filepath.IsAbs(opts.ProofFile)) {
		return config.ErrSecretUnavailable
	}
	if opts.proofRequested() {
		if opts.Timeout <= 0 {
			return config.ErrSecretUnavailable
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	if opts.proofRequested() && !hasSecretGrants(cfg) {
		return config.ErrSecretUnavailable
	}
	if hasSecretGrants(cfg) {
		opts.SafeSecrets = true
		restore := silenceSecretLogs()
		defer restore()
	}
	secret := requiresSecretPreflight(opts, cfg)
	var req secretfile.Request
	var evidence *secretfile.Result
	if secret {
		req, evidence, err = prepareSecrets(ctx, opts, cfg, nil, false, false)
		if err != nil {
			return err
		}
	}
	engine, err := openEngine(ctx, cfg, opts)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	sn := resolveStackNameWithOptions(cfg, opts)
	var result *plan.Plan
	var preview secretfile.PreviewState
	if opts.proofRequested() {
		result, preview, err = secretPreviewCompute(ctx, cfg, engine, sn, req, evidence, plan.PreviewOptions{ForceRecreate: opts.ForceRecreate, DryRun: opts.DryRun, Timeout: opts.Timeout})
	} else if secret {
		result, err = secretPlanCompute(ctx, cfg, engine, sn, req, evidence)
	} else {
		result, err = plan.Compute(ctx, cfg, engine, sn)
	}
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	if opts.operation != nil {
		opts.operation.planned(result, sn)
	} else if opts.proofRequested() {
		var out bytes.Buffer
		if *format == "json" {
			err = plan.RenderJSON(result, &out)
		} else {
			plan.Render(result, &out)
		}
		if err != nil {
			return config.ErrSecretUnavailable
		}
		if ctx.Err() != nil {
			return config.ErrSecretUnavailable
		}
		finish, err := secretPreviewPublish(req, evidence, opts.ProofKeyFile, opts.ProofFile, opts.ConfigFile, preview)
		if err != nil {
			return config.ErrSecretUnavailable
		}
		success := false
		defer func() { finish(success) }()
		if ctx.Err() != nil {
			return config.ErrSecretUnavailable
		}
		if _, err = os.Stdout.Write(out.Bytes()); err != nil {
			return config.ErrSecretUnavailable
		}
		success = true
	} else {
		switch *format {
		case "json":
			if err := plan.RenderJSON(result, os.Stdout); err != nil {
				return fmt.Errorf("rendering plan: %w", err)
			}
		default:
			plan.Render(result, os.Stdout)
		}
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

var secretPreviewCompute = plan.ComputePreview
var secretPreviewPublish = secretfile.PublishPreview
