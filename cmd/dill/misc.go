package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

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

func runValidate(ctx context.Context, configFile string) error {
	if err := config.Validate(ctx, configFile); err != nil {
		return err
	}
	fmt.Printf("%s is valid\n", configFile)
	return nil
}

func runFmt(ctx context.Context, configFile string) error {
	bin, err := loader.FindPkl(ctx)
	if err != nil {
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
