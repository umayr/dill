package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
)

func runStop(ctx context.Context, opts commandOptions, services []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	if err := fs.Parse(services); err != nil {
		return usageError{err.Error()}
	}
	services = fs.Args()

	st, err := resolveLabelStack(ctx, opts)
	if err != nil {
		return err
	}
	if len(services) > 0 {
		if err := st.requireConfig("stop"); err != nil {
			return err
		}
	}
	sn := st.Name
	engine, err := newEngine(ctx, st.Engine, opts)
	if err != nil {
		return err
	}
	defer engine.Close()

	var names []string
	if len(services) > 0 {
		cfg := st.Config
		if _, err := selectServices(cfg.Services, services, false); err != nil {
			return err
		}
		for _, svc := range services {
			names = append(names, serviceContainerName(sn, svc, cfg.Services[svc]))
		}
	} else {
		names, err = engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
		if len(names) == 0 {
			if !opts.Quiet {
				fmt.Printf("no containers found for stack %s\n", sn)
			}
			return nil
		}
	}

	var errs []error
	for _, name := range names {
		if opts.DryRun {
			fmt.Printf("would stop %s\n", name)
			continue
		}
		if !opts.Quiet {
			display := strings.TrimPrefix(name, sn+"_")
			printProgress(display, "stopping")
		}
		service := ""
		if len(services) > 0 {
			for _, svc := range services {
				if serviceContainerName(sn, svc, st.Config.Services[svc]) == name {
					service = svc
					break
				}
			}
		}
		id, err := orchestrator.OwnedID(ctx, engine, name, sn, service)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if id == "" {
			continue
		}
		if err := engine.StopService(ctx, id); err != nil && !errors.Is(err, orchestrator.ErrNotFound) {
			errs = append(errs, &orchestrator.ResourceError{Operation: "stop", Resource: name, ID: id, Err: err})
		}
	}
	return errors.Join(errs...)
}

// Direct start/restart never substitutes a prior plan for checked up.
func runStart(ctx context.Context, opts commandOptions, args []string) error {
	return runDirectStart(ctx, opts, args, false)
}
func runRestart(ctx context.Context, opts commandOptions, args []string) error {
	return runDirectStart(ctx, opts, args, true)
}

func runDirectStart(ctx context.Context, opts commandOptions, args []string, restart bool) (retErr error) {
	opts, restore := secretCommandOptions(opts, args)
	defer restore()
	defer func() {
		if opts.secretMode() {
			retErr = fixedSecretError(retErr)
		}
	}()
	name := "start"
	if restart {
		name = "restart"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	noDeps := fs.Bool("no-deps", false, "do not include service dependencies")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	if hasSecretGrants(cfg) {
		opts.SafeSecrets = true
	}
	selected, err := selectServices(cfg.Services, fs.Args(), !*noDeps)
	if err != nil {
		return err
	}
	for _, svc := range selected {
		if len(svc.Secrets) > 0 {
			return config.ErrSecretUnavailable
		}
	}
	if *noDeps {
		selected = withoutDependencies(selected)
	}
	g, err := dag.Build(selected)
	if err != nil {
		return err
	}
	engine, err := openEngine(ctx, cfg, opts)
	if err != nil {
		return err
	}
	defer engine.Close()
	stack := resolveStackNameWithOptions(cfg, opts)
	ids, err := orchestrator.DirectStartIDs(ctx, engine, stack, selected, len(fs.Args()) == 0)
	if err != nil {
		return config.ErrSecretUnavailable
	}
	if opts.DryRun {
		for service := range selected {
			fmt.Printf("would %s %s\n", name, service)
		}
		return nil
	}
	if restart {
		// Use immutable IDs from the pre-mutation ownership/classification pass.
		// Any stop failure aborts rather than proceeding with an uncertain restart.
		var failures []error
		for _, id := range ids {
			if err := engine.StopService(ctx, id); err != nil {
				failures = append(failures, err)
			}
		}
		if err := errors.Join(failures...); err != nil {
			return err
		}
	}
	actions := map[string]dag.Action{}
	for service := range selected {
		actions[service] = dag.ActionStart
	}
	return g.Run(ctx, engine, stack, opts.Timeout, actions, opts.progress(), dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}
