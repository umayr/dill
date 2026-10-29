package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/umayr/dill/internal/log"
)

func runPull(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(fs.Args(), "pull"); err != nil {
		return err
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if opts.Quiet {
		isTTY = false
	}
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
