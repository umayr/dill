package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

func runPull(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	var pullMu sync.Mutex
	mkSink := newMakePullSink(isTTY, &pullMu)

	g, gctx := errgroup.WithContext(ctx)
	for name, svc := range cfg.Services {
		name, svc := name, svc
		if svc.PullPolicy == "never" {
			logger.Debug("skipping pull (pull_policy=never)", "service", name)
			continue
		}
		g.Go(func() error {
			sink := mkSink(name)
			sink.Begin()
			err := engine.PullImage(gctx, svc.Image, sink)
			sink.Done()
			if err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			return nil
		})
	}
	return g.Wait()
}
