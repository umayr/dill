package main

import (
	"context"
	"errors"
	"flag"
	"github.com/umayr/dill/core"
)

func runJob(ctx context.Context, o commandOptions, args []string) error {
	fs := flag.NewFlagSet("job", flag.ContinueOnError)
	addConfigFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || o.DryRun || o.operation != nil || o.secretMode() {
		return usageError{"job requires exactly one job name and ordinary execution"}
	}
	cfg, err := loadConfig(ctx, o)
	if err != nil {
		return err
	}
	if err = validateTrustedStack(o, cfg); err != nil {
		return err
	}
	name := fs.Arg(0)
	if cfg.Jobs[name] == nil {
		return errors.New("unknown job")
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	engine, err := openEngine(ctx, cfg, o)
	if err != nil {
		return err
	}
	defer engine.Close()
	return core.RunHookJobs(ctx, engine, resolveStackNameWithOptions(cfg, o), cfg, []string{name})
}
