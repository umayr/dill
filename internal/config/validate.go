package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	pklgo "github.com/apple/pkl-go/pkl"
	"github.com/umayr/dill/internal/loader"
)

// Validate evaluates a .pkl config file using the pkl runtime and returns any
// type or syntax errors. Returns nil if the file is valid.
func Validate(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}

	bin, err := loader.FindPkl(ctx)
	if err != nil {
		return err
	}

	mgr := pklgo.NewEvaluatorManagerWithCommand([]string{bin})
	defer mgr.Close()

	ev, err := mgr.NewEvaluator(ctx, pklgo.PreconfiguredOptions)
	if err != nil {
		return fmt.Errorf("creating pkl evaluator: %w", err)
	}
	defer ev.Close()

	// EvaluateOutputText forces full module evaluation including type checks.
	// dill.pkl modules don't define custom output.text, so pkl uses its default
	// rendering — if any property violates a type constraint the error surfaces here.
	if _, err := ev.EvaluateOutputText(ctx, pklgo.FileSource(abs)); err != nil {
		var evalErr *pklgo.EvalError
		if errors.As(err, &evalErr) {
			return fmt.Errorf("%s", evalErr.ErrorOutput)
		}
		return fmt.Errorf("pkl evaluation: %w", err)
	}

	return nil
}
