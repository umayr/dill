package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/term"
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
	engine, err := openEngine(ctx, cfg, opts)
	if err != nil {
		return err
	}
	defer engine.Close()

	containerID, err := ownedServiceID(ctx, engine, sn, svc, cfg.Services[svc])
	if err != nil {
		return err
	}

	flags := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		flags = append(flags, "-t")
	}
	execArgs := append(append(flags, containerID), cmdArgs...)
	binary, execArgs := engineCommand(engine, execArgs...)
	c := exec.CommandContext(ctx, binary, execArgs...)
	c.Env = engineEnvironment(engine)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
