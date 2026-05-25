package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

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

	cmd := exec.CommandContext(ctx, pkl, "eval", "--format", "json", path)
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
	return r.Config, nil
}
