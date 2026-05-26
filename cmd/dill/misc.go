package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/loader"
)

func runInit(configFile string) error {
	if _, err := os.Stat(configFile); err == nil {
		return fmt.Errorf("%s already exists", configFile)
	}
	if err := os.WriteFile(configFile, []byte(starterTemplate), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configFile, err)
	}
	fmt.Printf("created %s\n", configFile)
	return nil
}

func runValidate(ctx context.Context, configFile string, args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	stdin := fs.Bool("stdin", false, "read config from stdin instead of file")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}

	target := configFile
	if *stdin {
		abs, err := filepath.Abs(configFile)
		if err != nil {
			abs = configFile
		}
		tmp, err := os.CreateTemp(filepath.Dir(abs), "dill-validate-*.pkl")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		defer os.Remove(tmp.Name())

		if _, err := io.Copy(tmp, os.Stdin); err != nil {
			tmp.Close()
			return fmt.Errorf("reading stdin: %w", err)
		}
		tmp.Close()
		target = tmp.Name()
	}

	validateErr := config.Validate(ctx, target)

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		if validateErr == nil {
			return enc.Encode(map[string]any{"valid": true})
		}
		return enc.Encode(map[string]any{"valid": false, "error": validateErr.Error()})
	}

	if validateErr != nil {
		return validateErr
	}
	fmt.Printf("%s is valid\n", configFile)
	return nil
}

func runFmt(ctx context.Context, configFile string, args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	stdin := fs.Bool("stdin", false, "read config from stdin, write formatted output to stdout")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}

	bin, err := loader.FindPkl(ctx)
	if err != nil {
		return err
	}

	if *stdin {
		tmp, err := os.CreateTemp("", "dill-fmt-*.pkl")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		defer os.Remove(tmp.Name())

		if _, err := io.Copy(tmp, os.Stdin); err != nil {
			tmp.Close()
			return fmt.Errorf("reading stdin: %w", err)
		}
		tmp.Close()

		cmd := exec.CommandContext(ctx, bin, "format", "-w", tmp.Name())
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pkl format: %w", err)
		}

		out, err := os.ReadFile(tmp.Name())
		if err != nil {
			return fmt.Errorf("reading formatted output: %w", err)
		}
		_, err = os.Stdout.Write(out)
		return err
	}

	cmd := exec.CommandContext(ctx, bin, "format", "-w", configFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pkl format: %w", err)
	}
	fmt.Printf("formatted %s\n", configFile)
	return nil
}
