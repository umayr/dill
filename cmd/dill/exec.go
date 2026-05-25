package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func runExec(ctx context.Context, configFile string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: dill exec <service> [command...]")
	}
	svc := args[0]
	cmdArgs := args[1:]
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"sh"}
	}

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	sn := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	// Resolve container name: try bare name first, fall back to stack-prefixed.
	containerName := svc
	if _, err := engine.ServiceStatus(ctx, svc); err != nil {
		if !errors.Is(err, orchestrator.ErrNotFound) {
			return fmt.Errorf("looking up service %q: %w", svc, err)
		}
		containerName = sn + "_" + svc
	}

	binary := engineBinary(engine)
	flags := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		flags = append(flags, "-t")
	}
	execArgs := append(append(flags, containerName), cmdArgs...)
	c := exec.CommandContext(ctx, binary, execArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
