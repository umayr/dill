package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/umayr/dill/internal/loader"
)

// InvalidError identifies invalid evaluated configuration independently of operational failures.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// root is the top-level pkl module output wrapping DillConfig.
type root struct {
	Config *DillConfig `json:"config"`
}

// Load evaluates a .pkl config file and returns the parsed DillConfig.
// The pkl binary is located automatically (see internal/pklbin).
func Load(ctx context.Context, path string) (*DillConfig, error) {
	pkl, err := loader.FindPkl(ctx)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, pkl, "eval", path)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("pkl eval: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &InvalidError{Err: fmt.Errorf("pkl eval failed:\n%s", exitErr.Stderr)}
		}
		return nil, fmt.Errorf("pkl eval: %w", err)
	}

	var r root
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, &InvalidError{Err: fmt.Errorf("parsing pkl output: %w", err)}
	}
	if r.Config == nil {
		return nil, &InvalidError{Err: fmt.Errorf("pkl output missing 'config' field")}
	}
	// Populate BaseDir on every service so relative bind-mount paths can be
	// resolved correctly regardless of the caller's working directory.
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	baseDir := filepath.Dir(absPath)
	for _, svc := range r.Config.Services {
		if svc != nil {
			svc.BaseDir = baseDir
		}
	}
	if err := ValidateRuntime(r.Config); err != nil {
		return nil, &InvalidError{Err: err}
	}
	return r.Config, nil
}
