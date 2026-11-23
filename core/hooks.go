package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
	"io"
	"sort"
	"sync"
	"time"
)

// RunHookJobs executes an explicit, deduplicated job set on an authorized engine.
// On an ambiguous start or wait, retain the exact container and never retry it.
func RunHookJobs(ctx context.Context, engine orchestrator.Engine, stack string, cfg *config.DillConfig, names []string) error {
	if len(names) == 0 {
		return nil
	}
	if engine == nil || cfg == nil || stack == "" {
		return errors.New("missing job execution context")
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		return err
	}
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return ErrSecretDeployUnsupported
	}
	if err := engine.EnsureNetwork(ctx, stack); err != nil {
		return err
	}
	r := jobRun{engine: engine, stack: stack, cfg: cfg}
	return r.run(ctx, names)
}

func hookChange(a dag.Action) string {
	if a == dag.ActionCreate {
		return "create"
	}
	return "replace"
}
func hookPrerequisites(c *config.DillConfig, h *config.LifecycleHooks) []string {
	seen := map[string]bool{}
	deps := map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		if j := c.Jobs[n]; j != nil {
			for _, d := range j.DependsOn {
				deps[d] = true
			}
			for _, d := range j.AfterJobs {
				visit(d)
			}
		}
	}
	for _, ev := range []string{"beforeUp"} {
		for _, ch := range []string{"create", "replace"} {
			for _, n := range config.MatchedJobs(h, ev, ch) {
				visit(n)
			}
		}
	}
	var out []string
	for n := range deps {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

type jobRun struct {
	mu        sync.Mutex
	engine    orchestrator.Engine
	stack     string
	cfg       *config.DillConfig
	completed map[string]error
}

func (r *jobRun) run(ctx context.Context, names []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.completed == nil {
		r.completed = map[string]error{}
	}
	for _, n := range names {
		if err := r.visit(ctx, n); err != nil {
			return err
		}
	}
	return nil
}
func (r *jobRun) visit(ctx context.Context, n string) error {
	if err, ok := r.completed[n]; ok {
		return err
	}
	r.completed[n] = errors.New("cyclic job dependency")
	j := r.cfg.Jobs[n]
	if j == nil {
		return fmt.Errorf("unknown job %q", n)
	}
	for _, d := range j.AfterJobs {
		if err := r.visit(ctx, d); err != nil {
			return err
		}
	}
	err := r.execute(ctx, n, j)
	r.completed[n] = err
	return err
}
func (r *jobRun) execute(ctx context.Context, n string, j *config.Job) error {
	e, ok := r.engine.(orchestrator.JobEngine)
	if !ok {
		return errors.New("engine does not support one-shot jobs")
	}
	if len(j.Volumes) > 0 {
		return errors.New("job mounts are not supported")
	}
	for _, d := range j.DependsOn {
		s := r.cfg.Services[d]
		if s == nil {
			return fmt.Errorf("unknown job dependency %q", d)
		}
		name := s.ContainerName
		if name == "" {
			name = r.stack + "_" + d
		}
		live, err := r.engine.InspectConfig(ctx, name)
		if err != nil {
			return err
		}
		if err = orchestrator.CheckOwnership(name, r.stack, d, live); err != nil {
			return err
		}
		ready, err := r.engine.IsReady(ctx, live.ID, s.HealthCheck != nil)
		if err != nil {
			return err
		}
		if !ready {
			return fmt.Errorf("job prerequisite %q not ready", d)
		}
	}
	exists, err := r.engine.ImageExists(ctx, j.Image)
	if err != nil {
		return err
	}
	if j.PullPolicy == "always" || (!exists && j.PullPolicy != "never") {
		if err = r.engine.PullImage(ctx, j.Image, io.Discard); err != nil {
			return fmt.Errorf("pull job %q image failed", n)
		}
	}
	env := make([]string, 0, len(j.Environment))
	for k, v := range j.Environment {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	id, err := e.CreateJob(ctx, orchestrator.JobSpec{Image: j.Image, Command: j.Command, Env: env, User: j.User, Network: r.stack})
	if err != nil {
		return fmt.Errorf("create job %q: %w", n, err)
	}
	if err = e.StartJob(ctx, id); err != nil {
		return fmt.Errorf("job %q outcome unknown; inspect %s: %w", n, id, err)
	}
	result, err := e.WaitJob(ctx, id)
	if err != nil || !result.Known {
		return fmt.Errorf("job %q outcome unknown; inspect %s", n, id)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("job %q exited %d; inspect %s", n, result.ExitCode, id)
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = e.RemoveJob(cleanup, id); err != nil {
		return fmt.Errorf("job %q succeeded; cleanup of %s failed", n, id)
	}
	return nil
}
