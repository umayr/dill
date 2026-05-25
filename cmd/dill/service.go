package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
)

func runStop(ctx context.Context, configFile string, services []string) error {
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

	var names []string
	if len(services) > 0 {
		for _, svc := range services {
			containerName := sn + "_" + svc
			if s, ok := cfg.Services[svc]; ok && s.ContainerName != "" {
				containerName = s.ContainerName
			}
			names = append(names, containerName)
		}
	} else {
		names, err = engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
		if len(names) == 0 {
			logger.Debug("no containers found", "stack", sn)
			return nil
		}
	}

	for _, name := range names {
		if err := engine.StopService(ctx, name); err != nil {
			logger.Warn("stop failed", "name", name, "err", err)
		}
	}
	return nil
}

// runStart starts all stopped containers in dependency order.
func runStart(ctx context.Context, configFile string, timeout time.Duration, services []string) error {
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

	svcs := cfg.Services
	if len(services) > 0 {
		svcs = filterServices(cfg.Services, services)
	}

	g, err := dag.Build(svcs)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	actions := make(map[string]dag.Action, len(svcs))
	for name := range svcs {
		actions[name] = dag.ActionStart
	}
	return g.Run(ctx, engine, sn, timeout, actions, printProgress,
		dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}

// runRestart stops all containers concurrently, then starts them in dependency order.
func runRestart(ctx context.Context, configFile string, timeout time.Duration, services []string) error {
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

	var names []string
	if len(services) > 0 {
		for _, svc := range services {
			containerName := sn + "_" + svc
			if s, ok := cfg.Services[svc]; ok && s.ContainerName != "" {
				containerName = s.ContainerName
			}
			names = append(names, containerName)
		}
	} else {
		names, err = engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
		if len(names) == 0 {
			logger.Debug("no containers found", "stack", sn)
			return nil
		}
	}

	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			display := strings.TrimPrefix(name, sn+"_")
			printProgress(display, "stopping")
			if err := engine.StopService(ctx, name); err != nil {
				logger.Warn("stop failed", "name", name, "err", err)
			}
		}()
	}
	wg.Wait()

	svcs := cfg.Services
	if len(services) > 0 {
		svcs = filterServices(cfg.Services, services)
	}

	g, err := dag.Build(svcs)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	actions := make(map[string]dag.Action, len(svcs))
	for name := range svcs {
		actions[name] = dag.ActionStart
	}
	return g.Run(ctx, engine, sn, timeout, actions, printProgress,
		dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}
