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
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("pkl eval failed:\n%s", exitErr.Stderr)
		}
		return nil, fmt.Errorf("pkl eval: %w", err)
	}

	var r root
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("parsing pkl output: %w", err)
	}
	if r.Config == nil {
		return nil, fmt.Errorf("pkl output missing 'config' field")
	}
	// Populate BaseDir on every service so relative bind-mount paths can be
	// resolved correctly regardless of the caller's working directory.
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	baseDir := filepath.Dir(absPath)
	for _, svc := range r.Config.Services {
		svc.BaseDir = baseDir
	}
	return r.Config, nil
}
