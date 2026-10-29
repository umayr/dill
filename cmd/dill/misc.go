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

func runInit(opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	addFileFlag(fs, &opts)
	addActionFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(fs.Args(), "init"); err != nil {
		return err
	}
	if opts.DryRun {
		fmt.Printf("would create %s\n", opts.ConfigFile)
		return nil
	}
	if _, err := os.Stat(opts.ConfigFile); err == nil {
		return fmt.Errorf("%s already exists", opts.ConfigFile)
	}
	if err := os.WriteFile(opts.ConfigFile, []byte(starterTemplate), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", opts.ConfigFile, err)
	}
	if !opts.Quiet {
		fmt.Printf("created %s\n", opts.ConfigFile)
	}
	return nil
}

func runValidate(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	addFileFlag(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	stdin := fs.Bool("stdin", false, "read config from stdin instead of file")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "validate"); err != nil {
		return err
	}

	target := opts.ConfigFile
	if *stdin {
		abs, err := filepath.Abs(opts.ConfigFile)
		if err != nil {
			abs = opts.ConfigFile
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
	fmt.Printf("%s is valid\n", opts.ConfigFile)
	return nil
}

func runFmt(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	addFileFlag(fs, &opts)
	addActionFlags(fs, &opts)
	stdin := fs.Bool("stdin", false, "read config from stdin, write formatted output to stdout")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(fs.Args(), "fmt"); err != nil {
		return err
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

	if opts.DryRun {
		fmt.Printf("would format %s\n", opts.ConfigFile)
		return nil
	}

	cmd := exec.CommandContext(ctx, bin, "format", "-w", opts.ConfigFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pkl format: %w", err)
	}
	if !opts.Quiet {
		fmt.Printf("formatted %s\n", opts.ConfigFile)
	}
	return nil
}
