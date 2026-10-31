package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"
	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

func runUp(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	fs.BoolVar(&opts.ForceRecreate, "force-recreate", opts.ForceRecreate, "force recreate containers even if nothing changed")
	noDeps := fs.Bool("no-deps", false, "do not include service dependencies when selecting services")
	pullPolicy := fs.String("pull", "", "temporarily override pull policy: always, missing, or never")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *pullPolicy != "" && *pullPolicy != "always" && *pullPolicy != "missing" && *pullPolicy != "never" {
		return usageError{fmt.Sprintf("unknown pull policy %q (supported: always, missing, never)", *pullPolicy)}
	}

	// Cancel the context on SIGINT/SIGTERM so the existing rollback path in
	// dag.Run fires automatically rather than leaving partial state.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	logger.Debug("loaded config", "engine", cfg.Engine, "services", len(cfg.Services))

	// If specific services are named, restrict to those plus their transitive deps.
	if len(fs.Args()) > 0 {
		selected, err := selectServices(cfg.Services, fs.Args(), !*noDeps)
		if err != nil {
			return err
		}
		cfg.Services = selected
	}
	if *pullPolicy != "" {
		for _, svc := range cfg.Services {
			svc.PullPolicy = *pullPolicy
		}
	}

	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	sn := resolveStackNameWithOptions(cfg, opts)
	logger.Debug("using stack name", "stack", sn)

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if opts.Quiet {
		isTTY = false
	}
	var pullMu sync.Mutex
	mkSink := newMakePullSink(isTTY, &pullMu)

	// pull_policy: always means "check for a new image on every up". Pull
	// before computing the plan so the image-ID comparison can detect the new
	// image and turn an otherwise unchanged service into a recreate.
	if !opts.DryRun {
		if err := pullAlwaysImages(ctx, engine, cfg.Services, mkSink); err != nil {
			return err
		}
	}

	p, err := plan.Compute(ctx, cfg, engine, sn)
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	// --force-recreate upgrades unchanged containers to recreate.
	if opts.ForceRecreate {
		for i := range p.Changes {
			if p.Changes[i].Kind == plan.KindNoop {
				p.Changes[i].Kind = plan.KindRecreate
			}
		}
	}

	if opts.DryRun {
		plan.Render(p, os.Stdout)
		return nil
	}

	// Remove orphaned containers first.
	for _, ch := range p.Changes {
		if ch.Kind != plan.KindRemove {
			continue
		}
		display := strings.TrimPrefix(ch.Service, sn+"_")
		if !opts.Quiet {
			printProgress(display, "removing orphan")
		}
		if err := engine.StopService(ctx, ch.Service); err != nil {
			logger.Debug("stop orphan failed", "container", ch.Service, "err", err)
		}
		if err := engine.RemoveService(ctx, ch.Service); err != nil {
			logger.Warn("remove orphan failed", "container", ch.Service, "err", err)
		}
	}

	actions := make(map[string]dag.Action, len(cfg.Services))
	for _, ch := range p.Changes {
		switch ch.Kind {
		case plan.KindCreate:
			actions[ch.Service] = dag.ActionCreate
		case plan.KindRecreate:
			actions[ch.Service] = dag.ActionRecreate
		case plan.KindNoop:
			actions[ch.Service] = dag.ActionNoop
		}
	}

	g, err := dag.Build(cfg.Services)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	return g.Run(ctx, engine, sn, opts.Timeout, actions, opts.progress(), mkSink)
}

// pullAlwaysImages pulls every service with pull_policy "always" before the
// plan is computed. Once the images are local, the policy is downgraded to
// "missing" so the DAG's create/recreate path does not pull them a second time.
func pullAlwaysImages(ctx context.Context, engine orchestrator.Engine, services map[string]*config.Service, mkSink dag.MakePullSink) error {
	g, gctx := errgroup.WithContext(ctx)
	for name, svc := range services {
		if svc.PullPolicy != "always" {
			continue
		}
		name, svc := name, svc
		g.Go(func() error {
			sink := mkSink(name)
			sink.Begin()
			defer sink.Done()
			if err := engine.PullImage(gctx, svc.Image, sink); err != nil {
				return fmt.Errorf("service %q: pulling %s: %w", name, svc.Image, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	for _, svc := range services {
		if svc.PullPolicy == "always" {
			svc.PullPolicy = "missing"
		}
	}
	return nil
}

// filterServices returns a copy of services containing only the named services
// and their transitive dependencies (BFS over DependsOn).
func filterServices(all map[string]*config.Service, names []string) map[string]*config.Service {
	included := make(map[string]bool)
	queue := append([]string{}, names...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if included[name] {
			continue
		}
		included[name] = true
		if svc, ok := all[name]; ok {
			queue = append(queue, svc.DependsOn...)
		}
	}
	out := make(map[string]*config.Service, len(included))
	for name := range included {
		if svc, ok := all[name]; ok {
			out[name] = svc
		}
	}
	return out
}
