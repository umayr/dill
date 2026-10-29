package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/orchestrator"
)

func runExec(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()
	if len(args) < 1 {
		return usageError{"usage: dill exec <service> [command...]"}
	}
	svc := args[0]
	cmdArgs := args[1:]
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"sh"}
	}

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	if _, ok := cfg.Services[svc]; !ok {
		return usageError{fmt.Sprintf("unknown service %q", svc)}
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
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
		containerName = serviceContainerName(sn, svc, cfg.Services[svc])
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
