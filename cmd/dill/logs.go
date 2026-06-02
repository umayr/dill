package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

func runLogs(ctx context.Context, configFile string, args []string) error {
	lfs := flag.NewFlagSet("logs", flag.ExitOnError)
	follow := lfs.Bool("f", false, "follow log output")
	tail := lfs.Int("n", 0, "number of lines to show from the end (0 = all)")
	if err := lfs.Parse(args); err != nil {
		return err
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

	// If a service name is provided, show only that container.
	// Resolve via config so we use the correct container name regardless of custom
	// container_name settings, and fail fast if the service doesn't exist.
	var names []string
	if lfs.NArg() > 0 {
		svcName := lfs.Arg(0)
		svc, ok := cfg.Services[svcName]
		if !ok {
			return fmt.Errorf("service %q is not defined in the config", svcName)
		}
		cn := svc.ContainerName
		if cn == "" {
			cn = sn + "_" + svcName
		}
		names = []string{cn}
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

	// Single container: stream directly with no prefix.
	if len(names) == 1 {
		return engine.Logs(ctx, names[0], *follow, *tail, os.Stdout)
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
			w := &prefixWriter{prefix: prefix, out: os.Stdout, mu: &mu}
			if err := engine.Logs(ctx, name, *follow, *tail, w); err != nil {
				logger.Warn("logs failed", "name", name, "err", err)
			}
			w.flush()
		}()
	}
	wg.Wait()
	return nil
}
