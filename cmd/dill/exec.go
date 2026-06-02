package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
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

	// Resolve container name via config so we honour custom container_name and
	// fail fast if the service doesn't exist instead of hitting a random container.
	s, ok := cfg.Services[svc]
	if !ok {
		return fmt.Errorf("service %q is not defined in the config", svc)
	}
	containerName := s.ContainerName
	if containerName == "" {
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
