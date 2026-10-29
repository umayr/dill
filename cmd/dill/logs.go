package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
)

func runLogs(ctx context.Context, opts commandOptions, args []string) error {
	lfs := flag.NewFlagSet("logs", flag.ContinueOnError)
	addConfigFlags(lfs, &opts)
	follow := lfs.Bool("follow", false, "follow log output")
	tail := lfs.Int("n", 0, "number of lines to show from the end (0 = all)")
	tailLong := lfs.Int("tail", 0, "number of lines to show from the end (0 = all)")
	since := lfs.String("since", "", "show logs since timestamp or engine-supported duration")
	timestamps := lfs.Bool("timestamps", false, "show log timestamps")
	noPrefix := lfs.Bool("no-prefix", false, "do not prefix lines when streaming multiple services")
	if err := lfs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *tailLong > 0 {
		*tail = *tailLong
	}

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

	// If a service name is provided, show only that container.
	var names []string
	if lfs.NArg() > 0 {
		if lfs.NArg() > 1 {
			return usageError{"usage: dill logs [flags] [service]"}
		}
		svc := lfs.Arg(0)
		if _, ok := cfg.Services[svc]; !ok {
			return usageError{fmt.Sprintf("unknown service %q", svc)}
		}
		names = []string{svc}
		if _, err := engine.ServiceStatus(ctx, svc); err != nil {
			if !errors.Is(err, orchestrator.ErrNotFound) {
				return fmt.Errorf("looking up service %q: %w", svc, err)
			}
			names = []string{serviceContainerName(sn, svc, cfg.Services[svc])}
		}
	} else {
		names, err = engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
	}

	if len(names) == 0 {
		logger.Debug("no containers found", "stack", sn)
		return nil
	}

	logOpts := orchestrator.LogOptions{Follow: *follow, Tail: *tail, Since: *since, Timestamps: *timestamps}

	// Single container: stream directly with no prefix.
	if len(names) == 1 {
		return engine.Logs(ctx, names[0], logOpts, os.Stdout)
	}

	// Multiple containers: stream concurrently, prefixing each line.
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, name := range names {
		name := name
		prefix := logPrefix(name, i, isTTY)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var w io.Writer = os.Stdout
			var pw *prefixWriter
			if !*noPrefix {
				pw = &prefixWriter{prefix: prefix, out: os.Stdout, mu: &mu}
				w = pw
			}
			if err := engine.Logs(ctx, name, logOpts, w); err != nil {
				logger.Warn("logs failed", "name", name, "err", err)
			}
			if pw != nil {
				pw.flush()
			}
		}()
	}
	wg.Wait()
	return nil
}
