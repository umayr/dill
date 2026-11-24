package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
	"golang.org/x/sync/errgroup"
)

// DeployEngine is the engine capability used by ordinary deployment.
// Its methods must honor each operation's context, including cleanup contexts.
type DeployEngine = orchestrator.Engine
type StackStatus = orchestrator.StackStatus
type LogOptions = orchestrator.LogOptions
type PullSink = dag.PullSink

var ErrSecretDeployUnsupported = errors.New("file-secret deployment is unsupported by core")

// DeployOptions supplies explicit operation settings and caller-owned callbacks.
// Open returns an engine owned by Deploy, which closes it. Callbacks must be safe
// for concurrent invocation; a nil PullSink discards pull progress.
type DeployOptions struct {
	Stack                                  string
	Services                               []string
	NoDeps, NoPrune, ForceRecreate, DryRun bool
	PullPolicy                             string
	Timeout                                time.Duration
	Open                                   func(context.Context, *Config) (DeployEngine, error)
	Progress                               func(string, string)
	PullSink                               func(string) PullSink
	Planned                                func(*Plan)
}

type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (discardSink) Begin()                      {}
func (discardSink) Done()                       {}

// Deploy performs ordinary, unqualified deployment, including ownership checks,
// orphan preflight, pulls and replan, and dependency-ordered execution. cfg must
// be normalized as documented on Config. The caller owns cfg and must not mutate
// it concurrently. Deploy copies service structs before changing pull policies.
// A returned error may follow successful mutations; no transactional rollback is
// promised. The caller must authorize the endpoint and coordinate operations.
// This API has no Brine known-secret records, downgrade guards, authorization or
// coordination; callers must enforce those policies independently. It must not
// be used for checked/qualified operations or wired into a service as a bypass.
func Deploy(ctx context.Context, cfg *Config, o DeployOptions) error {
	if ctx == nil || cfg == nil {
		return errors.New("context and config are required")
	}
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return ErrSecretDeployUnsupported
	}
	if o.Stack == "" || o.Open == nil {
		return errors.New("stack and engine opener are required")
	}
	if o.PullPolicy != "" && o.PullPolicy != "always" && o.PullPolicy != "missing" && o.PullPolicy != "never" {
		return fmt.Errorf("unknown pull policy %q", o.PullPolicy)
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		return err
	}
	if err := config.ValidateContainerNames(cfg.Services, o.Stack); err != nil {
		return err
	}
	// Copy every service so overrides and pull downgrades never mutate caller state.
	copyCfg := *cfg
	copyCfg.Services = make(map[string]*config.Service, len(cfg.Services))
	for n, s := range cfg.Services {
		cp := *s
		digest, err := config.DownHookDigest(cfg, n)
		if err != nil {
			return err
		}
		cp.DownHookDigest = digest
		copyCfg.Services[n] = &cp
	}
	selected := make(map[string]*config.Service)
	var add func(string) error
	add = func(n string) error {
		s, ok := copyCfg.Services[n]
		if !ok {
			return fmt.Errorf("unknown service %q", n)
		}
		if _, ok := selected[n]; ok {
			return nil
		}
		selected[n] = s
		for _, dep := range hookPrerequisites(cfg, s.Hooks) {
			if err := add(dep); err != nil {
				return err
			}
		}
		if !o.NoDeps {
			for _, dep := range s.DependsOn {
				if err := add(dep); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(o.Services) == 0 {
		for n, s := range copyCfg.Services {
			selected[n] = s
		}
	} else {
		for _, n := range o.Services {
			if err := add(n); err != nil {
				return err
			}
		}
	}
	if o.NoDeps && len(o.Services) > 0 {
		for n, s := range selected {
			cp := *s
			cp.DependsOn = nil
			selected[n] = &cp
		}
	}
	if o.PullPolicy != "" {
		for _, s := range selected {
			s.PullPolicy = o.PullPolicy
		}
	}
	if err := orchestrator.ValidateCapabilities(cfg.Engine, selected); err != nil {
		return fmt.Errorf("capability preflight: %w", err)
	}
	for n, s := range selected {
		cp := *s
		cp.DependsOn = append(append([]string(nil), s.DependsOn...), hookPrerequisites(cfg, s.Hooks)...)
		selected[n] = &cp
	}
	graph, err := dag.Build(selected)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	engine, err := o.Open(ctx, &copyCfg)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	if engine == nil || (reflect.ValueOf(engine).Kind() == reflect.Ptr && reflect.ValueOf(engine).IsNil()) {
		return errors.New("initialising engine: nil engine")
	}
	defer engine.Close()
	sink := dag.MakePullSink(func(n string) dag.PullSink {
		if o.PullSink != nil {
			if s := o.PullSink(n); s != nil {
				return s
			}
		}
		return discardSink{}
	})
	compute := func() (*plan.Plan, error) { return plan.Compute(ctx, &copyCfg, engine, o.Stack) }
	p, err := compute()
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}
	intent := &orchestrator.RemovalIntent{Stack: o.Stack}
	if len(o.Services) == 0 && !o.NoPrune {
		targets := []orchestrator.RemovalTarget{}
		for _, ch := range p.Changes {
			if ch.Kind == plan.KindRemove {
				targets = append(targets, orchestrator.RemovalTarget{Name: ch.Service})
			}
		}
		intent, err = orchestrator.CaptureRemovalIntent(ctx, engine, o.Stack, targets)
		if err != nil {
			return err
		}
	}
	if !o.DryRun {
		hasAlways := false
		for _, s := range selected {
			if s.PullPolicy == "always" {
				hasAlways = true
				break
			}
		}
		if hasAlways {
			group, gctx := errgroup.WithContext(ctx)
			for n, s := range selected {
				if s.PullPolicy != "always" {
					continue
				}
				n, s := n, s
				group.Go(func() error {
					out := sink(n)
					out.Begin()
					defer out.Done()
					if err := engine.PullImage(gctx, s.Image, out); err != nil {
						return fmt.Errorf("service %q: pulling %s: %w", n, s.Image, err)
					}
					return nil
				})
			}
			if err := group.Wait(); err != nil {
				return err
			}
			for _, s := range selected {
				if s.PullPolicy == "always" {
					s.PullPolicy = "missing"
				}
			}
			p, err = compute()
			if err != nil {
				return fmt.Errorf("computing plan after pull: %w", err)
			}
		}
	}
	filtered := &plan.Plan{Volumes: p.Volumes, Changes: make([]plan.Change, 0, len(p.Changes))}
	for _, ch := range p.Changes {
		if ch.Kind == plan.KindRemove {
			if len(o.Services) == 0 && !o.NoPrune {
				filtered.Changes = append(filtered.Changes, ch)
			}
			continue
		}
		if _, ok := selected[ch.Service]; ok {
			if o.ForceRecreate && ch.Kind == plan.KindNoop {
				ch.Kind = plan.KindRecreate
			}
			filtered.Changes = append(filtered.Changes, ch)
		}
	}
	filtered.PopulateHooks(cfg)
	if o.Planned != nil {
		o.Planned(filtered)
	}
	if o.DryRun {
		return nil
	}
	var errs []error
	for _, r := range intent.Resources {
		if o.Progress != nil {
			o.Progress(strings.TrimPrefix(r.Name, o.Stack+"_"), "removing orphan")
		}
		if err := intent.Remove(ctx, engine, r); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	actions := make(map[string]dag.Action, len(selected))
	for _, ch := range filtered.Changes {
		switch ch.Kind {
		case plan.KindCreate:
			actions[ch.Service] = dag.ActionCreate
		case plan.KindRecreate:
			actions[ch.Service] = dag.ActionRecreate
		case plan.KindNoop:
			actions[ch.Service] = dag.ActionNoop
		}
	}
	before := jobRun{cfg: cfg, engine: engine, stack: o.Stack}
	var post []string
	var ordered []string
	for n := range actions {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	for _, n := range ordered {
		a := actions[n]
		if a == dag.ActionNoop {
			continue
		}
		post = append(post, config.MatchedJobs(selected[n].Hooks, "afterUp", hookChange(a))...)
	}
	graph.BeforeAction(func(c context.Context, n string, a dag.Action) error {
		if a == dag.ActionNoop {
			return nil
		}
		return before.run(c, config.MatchedJobs(selected[n].Hooks, "beforeUp", hookChange(a)))
	})
	runCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if err := graph.Run(runCtx, engine, o.Stack, o.Timeout, actions, o.Progress, sink); err != nil {
		return err
	}
	after := jobRun{cfg: cfg, engine: engine, stack: o.Stack}
	if err := after.run(runCtx, post); err != nil {
		return fmt.Errorf("applied; verification failed (a no-op up does not rerun checks; inspect the target and use dill job for reviewed verification): %w", err)
	}
	return nil
}

// OpenDeployEngine opens an explicitly selected endpoint. Caller owns the result.
func OpenDeployEngine(ctx context.Context, engine, endpoint string) (DeployEngine, error) {
	if endpoint == "" {
		return nil, errors.New("deploy engine endpoint is required")
	}
	switch engine {
	case "docker":
		return orchestrator.NewDockerEngineAt(endpoint)
	case "podman":
		return orchestrator.NewPodmanEngine(ctx, endpoint)
	default:
		return nil, fmt.Errorf("unknown engine %q", engine)
	}
}
