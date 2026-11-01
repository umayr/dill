package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"sync"

	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
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
	engine, err := newEngine(ctx, st.Engine)
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

	for _, name := range names {
		if opts.DryRun {
			fmt.Printf("would stop %s\n", name)
			continue
		}
		if !opts.Quiet {
			display := strings.TrimPrefix(name, sn+"_")
			printProgress(display, "stopping")
		}
		if err := engine.StopService(ctx, name); err != nil {
			logger.Warn("stop failed", "name", name, "err", err)
		}
	}
	return nil
}

// runStart starts all stopped containers in dependency order.
func runStart(ctx context.Context, opts commandOptions, services []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	noDeps := fs.Bool("no-deps", false, "do not include service dependencies when selecting services")
	if err := fs.Parse(services); err != nil {
		return usageError{err.Error()}
	}
	services = fs.Args()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()

	svcs := cfg.Services
	if len(services) > 0 {
		svcs, err = selectServices(cfg.Services, services, !*noDeps)
		if err != nil {
			return err
		}
	}

	g, err := dag.Build(svcs)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	actions := make(map[string]dag.Action, len(svcs))
	for name := range svcs {
		actions[name] = dag.ActionStart
	}
	if opts.DryRun {
		for name := range svcs {
			fmt.Printf("would start %s\n", name)
		}
		return nil
	}
	return g.Run(ctx, engine, sn, opts.Timeout, actions, opts.progress(),
		dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}

// runRestart stops all containers concurrently, then starts them in dependency order.
func runRestart(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	noDeps := fs.Bool("no-deps", false, "do not include service dependencies when selecting services")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	services := fs.Args()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()

	var names []string
	if len(services) > 0 {
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

	if opts.DryRun {
		for _, name := range names {
			fmt.Printf("would restart %s\n", name)
		}
		return nil
	}

	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			display := strings.TrimPrefix(name, sn+"_")
			if !opts.Quiet {
				printProgress(display, "stopping")
			}
			if err := engine.StopService(ctx, name); err != nil {
				logger.Warn("stop failed", "name", name, "err", err)
			}
		}()
	}
	wg.Wait()

	svcs := cfg.Services
	if len(services) > 0 {
		svcs, err = selectServices(cfg.Services, services, !*noDeps)
		if err != nil {
			return err
		}
	}

	g, err := dag.Build(svcs)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	actions := make(map[string]dag.Action, len(svcs))
	for name := range svcs {
		actions[name] = dag.ActionStart
	}
	return g.Run(ctx, engine, sn, opts.Timeout, actions, opts.progress(),
		dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}
