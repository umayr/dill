package main

import (
	"context"
	"errors"
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
	"github.com/umayr/dill/internal/secretfile"
)

func runUp(ctx context.Context, opts commandOptions, args []string) (retErr error) {
	opts, restore := secretCommandOptions(opts, args)
	defer restore()
	defer func() {
		if opts.secretMode() {
			retErr = fixedSecretError(retErr)
		}
	}()

	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	fs.BoolVar(&opts.ForceRecreate, "force-recreate", opts.ForceRecreate, "force recreate containers even if nothing changed")
	fs.BoolVar(&opts.NoPrune, "no-prune", opts.NoPrune, "retain omitted services during full-stack up")
	fs.BoolVar(&opts.AlphaDisposable, "alpha-disposable", opts.AlphaDisposable, "restrict up to the isolated disposable-volume Alpha profile (implies no-prune)")
	noDeps := fs.Bool("no-deps", false, "do not include service dependencies when selecting services")
	pullPolicy := fs.String("pull", "", "temporarily override pull policy: always, missing, or never")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *pullPolicy != "" && *pullPolicy != "always" && *pullPolicy != "missing" && *pullPolicy != "never" {
		return usageError{fmt.Sprintf("unknown pull policy %q (supported: always, missing, never)", *pullPolicy)}
	}

	// Cancel on SIGINT/SIGTERM so dag.Run performs bounded failure cleanup.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	return runUpConfig(ctx, opts, cfg, fs.Args(), *noDeps, *pullPolicy, func(ctx context.Context, cfg *config.DillConfig) (orchestrator.Engine, error) {
		return openEngine(ctx, cfg, opts)
	})
}

func runUpConfig(ctx context.Context, opts commandOptions, cfg *config.DillConfig, names []string, noDeps bool, pullPolicy string, open func(context.Context, *config.DillConfig) (orchestrator.Engine, error)) (retErr error) {
	secret := requiresSecretPreflight(opts, cfg)
	safe := opts.secretMode() || secret
	if safe {
		restore := silenceSecretLogs()
		defer restore()
		opts.Quiet = true
		defer func() { retErr = fixedSecretError(retErr) }()
	}

	if err := validateTrustedStack(opts, cfg); err != nil {
		return err
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		return err
	}

	if opts.AlphaDisposable {
		if err := validateDisposableTarget(opts, cfg); err != nil {
			return err
		}
		opts.NoPrune = true
		if err := orchestrator.ValidateDisposableConfig(cfg); err != nil {
			return err
		}
	}

	logger.Debug("loaded config", "engine", cfg.Engine, "services", len(cfg.Services))

	// The plan is always computed against the full config so a partial `up`
	// cannot mistake unselected services for orphans. Only the selected subset
	// is planned, pulled, and run.
	var err error
	fullServices := cfg.Services
	selected := fullServices
	subset := len(names) > 0
	if subset {
		selected, err = selectServices(cfg.Services, names, !noDeps)
		if err != nil {
			return err
		}
		if noDeps {
			selected = withoutDependencies(selected)
		}
	}
	if pullPolicy != "" {
		for _, svc := range selected {
			svc.PullPolicy = pullPolicy
		}
	}

	if err := orchestrator.ValidateCapabilities(cfg.Engine, selected); err != nil {
		return fmt.Errorf("capability preflight: %w", err)
	}

	g, err := dag.Build(selected)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	var req secretfile.Request
	var evidence *secretfile.Result
	if secret {
		req, evidence, err = prepareSecrets(ctx, opts, cfg, names, noDeps, false)
		if err != nil {
			return err
		}
	}

	engine, err := open(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	if opts.AlphaDisposable {
		engine, err = orchestrator.WithDisposableGuardrails(ctx, engine, cfg, resolveStackNameWithOptions(cfg, opts))
		if err != nil {
			return err
		}
	}

	if secret {
		engine, err = secretEngineWrap(engine, req, evidence)
		if err != nil {
			return err
		}
	}

	sn := resolveStackNameWithOptions(cfg, opts)
	logger.Debug("using stack name", "stack", sn)

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if opts.Quiet {
		isTTY = false
	}
	var pullMu sync.Mutex
	mkSink := newMakePullSink(isTTY, &pullMu)
	if safe {
		mkSink = secretPullSink
	}

	// Check ownership before pulling images or applying any planned mutations.
	compute := func() (*plan.Plan, error) {
		if secret {
			return secretPlanCompute(ctx, cfg, engine, sn, req, evidence)
		}
		return plan.Compute(ctx, cfg, engine, sn)
	}
	p, err := compute()
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}
	orphanIntent := &orchestrator.RemovalIntent{Stack: sn}
	if !subset && !opts.NoPrune {
		orphanIntent, err = preflightOrphans(ctx, engine, p, sn)
		if err != nil {
			return err
		}
	}
	if !opts.DryRun {
		for _, svc := range selected {
			if svc.PullPolicy == "always" {
				if err := pullAlwaysImages(ctx, engine, selected, mkSink); err != nil {
					return err
				}
				// Recompute after pulling so moved tags still trigger recreation.
				p, err = compute()
				if err != nil {
					return fmt.Errorf("computing plan after pull: %w", err)
				}
				break
			}
		}
	}
	// A partial up only applies changes for the selected services. Orphan
	// removal remains a full-stack operation.
	p = filterPlan(p, selected, !subset && !opts.NoPrune)

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

	// Remove orphaned containers first. Do not proceed with deployment if any
	// orphan operation fails, but attempt other independent orphans.
	var orphanErrors []error
	for _, resource := range orphanIntent.Resources {
		display := strings.TrimPrefix(resource.Name, sn+"_")
		if !opts.Quiet {
			printProgress(display, "removing orphan")
		}
		if err := orphanIntent.Remove(ctx, engine, resource); err != nil {
			orphanErrors = append(orphanErrors, err)
		}
	}
	if err := errors.Join(orphanErrors...); err != nil {
		return err
	}

	actions := make(map[string]dag.Action, len(selected))
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

	return g.Run(ctx, engine, sn, opts.Timeout, actions, opts.progress(), mkSink)
}

// filterPlan keeps changes for the selected services. When keepOrphans is
// false (a partial up), orphan removals are dropped so unselected services are
// never touched.
func filterPlan(p *plan.Plan, selected map[string]*config.Service, keepOrphans bool) *plan.Plan {
	out := &plan.Plan{Changes: make([]plan.Change, 0, len(p.Changes))}
	for _, ch := range p.Changes {
		if ch.Kind == plan.KindRemove {
			if keepOrphans {
				out.Changes = append(out.Changes, ch)
			}
			continue
		}
		if _, ok := selected[ch.Service]; ok {
			out.Changes = append(out.Changes, ch)
		}
	}
	return out
}

// withoutDependencies returns copies of services with dependsOn cleared, so
// `up --no-deps <service>` can build a graph without the dependencies.
func withoutDependencies(services map[string]*config.Service) map[string]*config.Service {
	out := make(map[string]*config.Service, len(services))
	for name, svc := range services {
		cp := *svc
		cp.DependsOn = nil
		out[name] = &cp
	}
	return out
}

// pullAlwaysImages pulls every service with pullPolicy "always" before the
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

func preflightOrphans(ctx context.Context, e orchestrator.Engine, p *plan.Plan, stack string) (*orchestrator.RemovalIntent, error) {
	targets := []orchestrator.RemovalTarget{}
	for _, ch := range p.Changes {
		if ch.Kind == plan.KindRemove {
			targets = append(targets, orchestrator.RemovalTarget{Name: ch.Service})
		}
	}
	return orchestrator.CaptureRemovalIntent(ctx, e, stack, targets)
}

var secretPlanCompute = plan.ComputeChecked
var secretEngineWrap = orchestrator.WithSecrets
