package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/umayr/dill/internal/loader"
)

// InvalidError identifies invalid evaluated configuration independently of operational failures.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// EvaluatorError is a Pkl process failure whose cause cannot be reliably classified.
type EvaluatorError struct{ Err error }

func (e *EvaluatorError) Error() string { return e.Err.Error() }
func (e *EvaluatorError) Unwrap() error { return e.Err }

// Load evaluates a .pkl config file and returns the parsed DillConfig.
// The pkl binary is located automatically (see internal/pklbin).
func Load(ctx context.Context, path string) (result *DillConfig, retErr error) {
	private, _ := ctx.Value(privateEvaluationKey{}).(bool)
	if private {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	var cfg DillConfig
	defer func() {
		if private && retErr != nil {
			retErr = ErrSecretUnavailable
			return
		}
		for _, svc := range cfg.Services {
			if svc != nil && len(svc.Secrets) > 0 && retErr != nil {
				retErr = ErrSecretUnavailable
				return
			}
		}
	}()

	pkl, err := loader.FindPkl(ctx)
	if err != nil {
		return nil, err
	}

	var out []byte
	if private {
		out, err = privatePklOutput(ctx, pkl, path)
		if err != nil {
			return nil, ErrSecretUnavailable
		}
	} else {
		cmd := exec.CommandContext(ctx, pkl, "eval", path)
		out, err = cmd.Output()
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("pkl eval: %w", ctx.Err())
			}
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil, &EvaluatorError{Err: fmt.Errorf("pkl eval failed:\n%s", exitErr.Stderr)}
			}
			return nil, fmt.Errorf("pkl eval: %w", err)
		}

	}

	if err := rejectDuplicateKeys(out); err != nil {
		return nil, &InvalidError{Err: fmt.Errorf("parsing pkl output: %w", err)}
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, &InvalidError{Err: fmt.Errorf("parsing pkl output: %w", err)}
	}
	if len(r["config"]) == 0 || string(r["config"]) == "null" {
		return nil, &InvalidError{Err: fmt.Errorf("pkl output missing 'config' field")}
	}
	if err := decodeStrict(r["config"], &cfg); err != nil {
		return nil, &InvalidError{Err: fmt.Errorf("parsing pkl output: %w", err)}
	}
	if err := validateDecodedShape(r["config"], &cfg); err != nil {
		return nil, &InvalidError{Err: err}
	}
	// Populate BaseDir on every service so relative bind-mount paths can be
	// resolved correctly regardless of the caller's working directory.
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	baseDir := filepath.Dir(absPath)
	for _, svc := range cfg.Services {
		if svc != nil {
			svc.BaseDir = baseDir
		}
	}
	if err := ValidateRuntime(&cfg); err != nil {
		return nil, &InvalidError{Err: err}
	}
	return &cfg, nil
}
