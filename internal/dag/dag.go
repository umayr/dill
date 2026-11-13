package dag

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
)

// Action describes what up should do for a given service.
type Action int

const (
	ActionCreate   Action = iota // container does not exist; create fresh
	ActionRecreate               // container exists but config changed; stop+remove+create
	ActionNoop                   // container exists, config matches, already running; ensure it stays up
	ActionStart                  // container exists and is stopped; start it (used by start/restart)
)

// Progress is called to report user-visible status changes for a service.
// Pass nil to suppress progress output (e.g. during failure cleanup).
type Progress func(service, status string)

// PullSink receives streaming download progress for a single image pull.
// Begin is called immediately before the pull starts so the sink can print
// an initial status line. Done is called when the pull completes (success
// or failure) so the sink can finalise the line (e.g. append a newline).
type PullSink interface {
	io.Writer
	Begin()
	Done()
}

// MakePullSink constructs a PullSink for a named service. Run receives one
// from the caller so that main.go owns all terminal/formatting decisions.
type MakePullSink func(service string) PullSink

type node struct {
	name string
	svc  *config.Service
	deps []*node
	done chan struct{} // closed once the container is confirmed UP
}

// Graph is a validated dependency graph of services.
type Graph struct {
	nodes []*node
	index map[string]*node
}

// Build constructs a Graph from the services map.
// Returns an error if any dependsOn reference is missing or a cycle is detected.
func Build(services map[string]*config.Service) (*Graph, error) {
	g := &Graph{
		nodes: make([]*node, 0, len(services)),
		index: make(map[string]*node, len(services)),
	}

	// First pass: create all nodes.
	for name, svc := range services {
		n := &node{
			name: name,
			svc:  svc,
			done: make(chan struct{}),
		}
		g.nodes = append(g.nodes, n)
		g.index[name] = n
	}

	// Second pass: wire dependencies.
	for _, n := range g.nodes {
		for _, dep := range n.svc.DependsOn {
			d, ok := g.index[dep]
			if !ok {
				return nil, fmt.Errorf("service %q depends on %q which is not defined", n.name, dep)
			}
			n.deps = append(n.deps, d)
		}
	}

	// Validate: detect cycles via DFS.
	if err := detectCycles(g); err != nil {
		return nil, err
	}

	return g, nil
}

// Run starts all services according to the provided action map. Services with
// no shared dependencies start concurrently; each goroutine waits for its
// dependencies to be ready before acting.
//
// If any service fails to become ready within timeout, the entire deployment
// is aborted and all containers started during this run are torn down.
func (g *Graph) Run(ctx context.Context, engine orchestrator.Engine, stackName string, timeout time.Duration, actions map[string]Action, prog Progress, mkSink MakePullSink) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := engine.EnsureNetwork(ctx, stackName); err != nil {
		return fmt.Errorf("creating stack network: %w", err)
	}

	var mu sync.Mutex
	var started []string // immutable container IDs created during this run (for failure cleanup)
	var failures []error

	eg, egCtx := errgroup.WithContext(ctx)
	recordFailure := func(run func() error) func() error {
		return func() error {
			err := run()
			if err != nil {
				mu.Lock()
				failures = append(failures, err)
				mu.Unlock()
			}
			return err
		}
	}

	for _, n := range g.nodes {
		n := n
		eg.Go(recordFailure(func() error {
			// Wait for all dependencies to be UP.
			for _, dep := range n.deps {
				select {
				case <-dep.done:
					logger.Debug("dependency ready", "service", n.name, "dep", dep.name)
				case <-egCtx.Done():
					return fmt.Errorf("service %q: waiting for dependency %q: %w", n.name, dep.name, egCtx.Err())
				}
			}

			containerName := n.svc.ContainerName
			if containerName == "" {
				containerName = stackName + "_" + n.name
			}

			emit := func(status string) {
				if prog != nil {
					prog(n.name, status)
				}
			}

			// Recheck before acting and bind mutations to the inspected ID, so a
			// replacement container cannot be targeted by reusing the same name.
			containerID := containerName
			if action := actions[n.name]; action == ActionNoop || action == ActionStart || action == ActionRecreate {
				live, err := engine.InspectConfig(egCtx, containerName)
				if err != nil {
					return fmt.Errorf("service %q: inspect before reconciliation: %w", n.name, err)
				}
				if err := orchestrator.CheckOwnership(containerName, stackName, n.name, live); err != nil {
					return err
				}
				if live.ID == "" {
					return fmt.Errorf("service %q: inspect returned an empty container ID", n.name)
				}
				containerID = live.ID
			}

			switch actions[n.name] {
			case ActionNoop:
				emit("unchanged")
				if err := engine.StartExisting(egCtx, containerID); err != nil {
					return &orchestrator.ResourceError{Operation: "start service " + n.name, Resource: containerName, ID: containerID, Err: err}
				}

			case ActionStart:
				emit("starting")
				if err := engine.StartExisting(egCtx, containerID); err != nil {
					return &orchestrator.ResourceError{Operation: "start service " + n.name, Resource: containerName, ID: containerID, Err: err}
				}

			case ActionRecreate:
				emit("stopping")
				if err := engine.StopService(egCtx, containerID); err != nil {
					return &orchestrator.ResourceError{Operation: "stop before recreate", Resource: containerName, ID: containerID, Err: err}
				}
				if err := engine.RemoveService(egCtx, containerID); err != nil {
					return fmt.Errorf("service %q: remove before recreate: %w", n.name, err)
				}
				if err := maybePull(egCtx, engine, n.svc, mkSink(n.name)); err != nil {
					return fmt.Errorf("service %q: %w", n.name, err)
				}
				emit("recreating")
				id, err := engine.StartService(egCtx, n.name, n.svc, stackName)
				if err != nil {
					return fmt.Errorf("service %q: %w", n.name, err)
				}
				mu.Lock()
				started = append(started, id)
				mu.Unlock()
				containerID = id

			default: // ActionCreate
				if err := maybePull(egCtx, engine, n.svc, mkSink(n.name)); err != nil {
					return fmt.Errorf("service %q: %w", n.name, err)
				}
				emit("creating")
				id, err := engine.StartService(egCtx, n.name, n.svc, stackName)
				if err != nil {
					return fmt.Errorf("service %q: %w", n.name, err)
				}
				mu.Lock()
				started = append(started, id)
				mu.Unlock()
				containerID = id
			}

			// Poll until the container is ready.
			hasHealthCheck := n.svc.HealthCheck != nil
			pollStart := time.Now()
			var lastWaitEmit time.Time
			for {
				ready, err := engine.IsReady(egCtx, containerID, hasHealthCheck)
				if err != nil {
					return fmt.Errorf("service %q readiness check: %w", n.name, err)
				}
				// A readiness response can race cancellation, including when an
				// engine returns success after the request was canceled.
				if err := egCtx.Err(); err != nil {
					return fmt.Errorf("service %q readiness check: %w", n.name, err)
				}
				if ready {
					emit("ready")
					close(n.done)
					return nil
				}
				elapsed := time.Since(pollStart)
				if elapsed >= 5*time.Second && time.Since(lastWaitEmit) >= 5*time.Second {
					emit(fmt.Sprintf("waiting (%s)", elapsed.Round(time.Second)))
					lastWaitEmit = time.Now()
				}
				select {
				case <-time.After(500 * time.Millisecond):
				case <-egCtx.Done():
					return fmt.Errorf("service %q did not become ready: %w", n.name, egCtx.Err())
				}
			}
		}))
	}

	err := eg.Wait()
	// All workers can finish successfully while cancellation arrives after the
	// final readiness check. Do not turn that cancellation into success.
	if err == nil && ctx.Err() != nil {
		err = fmt.Errorf("deployment canceled after readiness: %w", ctx.Err())
		failures = append(failures, err)
	}
	if err != nil {
		logger.Warn("deployment failed, tearing down started services", "err", err)
		teardownCtx, teardownCancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer teardownCancel()
		failures = append(failures, teardown(teardownCtx, engine, started, nil))
		return errors.Join(failures...)
	}
	return nil
}

// Down queries the engine for all containers in the stack and stops + removes them.
// It does not require the config file — it relies solely on the dill.stack label.
func Down(ctx context.Context, engine orchestrator.Engine, stackName string, prog Progress) error {
	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack %q: %w", stackName, err)
	}
	intent := &orchestrator.RemovalIntent{Stack: stackName}
	var errs []error
	for _, name := range names {
		captured, err := orchestrator.CaptureRemovalIntent(ctx, engine, stackName, []orchestrator.RemovalTarget{{Name: name}})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		intent.Resources = append(intent.Resources, captured.Resources...)
	}
	// Preserve legacy best-effort independent cleanup while never authorizing a
	// failed ownership target. All identities are captured before any mutation.
	errs = append(errs, DownIntent(ctx, engine, intent, prog))
	return errors.Join(errs...)
}

// DownIntent applies only identities captured before the caller confirmed.
// Network removal retains the existing conservative adapter behavior.
func DownIntent(ctx context.Context, engine orchestrator.Engine, intent *orchestrator.RemovalIntent, prog Progress) error {
	var errs []error
	for _, resource := range intent.Resources {
		name := resource.Name
		if prog != nil {
			prog(name, "removing")
		}
		if err := intent.Remove(ctx, engine, resource); err != nil {
			errs = append(errs, err)
		}
	}
	if err := engine.RemoveNetwork(ctx, intent.Stack); err != nil {
		errs = append(errs, &orchestrator.ResourceError{Operation: "remove network (resource may be retained)", Resource: intent.Stack, Err: err})
	}
	return errors.Join(errs...)
}

// teardown stops and removes created container IDs during failure cleanup.
// prog is called with (name, "removing") for each container; pass nil to suppress.
func teardown(ctx context.Context, engine orchestrator.Engine, names []string, prog Progress) error {
	var errs []error
	for _, id := range names {
		if prog != nil {
			prog(id, "removing")
		}
		if err := orchestrator.RemoveContainer(ctx, engine, id, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// maybePull pulls the image based on the service's pullPolicy.
// sink.Begin/Done bracket the pull so the caller can render progress.
func maybePull(ctx context.Context, engine orchestrator.Engine, svc *config.Service, sink PullSink) error {
	pull := func() error {
		sink.Begin()
		defer sink.Done()
		return engine.PullImage(ctx, svc.Image, sink)
	}
	switch svc.PullPolicy {
	case "always":
		return pull()
	case "never":
		return nil
	default: // "missing" is the default
		exists, err := engine.ImageExists(ctx, svc.Image)
		if err != nil {
			return fmt.Errorf("checking image existence: %w", err)
		}
		if !exists {
			return pull()
		}
		logger.Debug("image already present, skipping pull", "image", svc.Image)
		return nil
	}
}

// detectCycles runs DFS from every node and returns an error on first cycle found.
func detectCycles(g *Graph) error {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(g.nodes))

	var dfs func(n *node) error
	dfs = func(n *node) error {
		switch state[n.name] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("cycle detected involving service %q", n.name)
		}
		state[n.name] = visiting
		for _, dep := range n.deps {
			if err := dfs(dep); err != nil {
				return err
			}
		}
		state[n.name] = visited
		return nil
	}

	for _, n := range g.nodes {
		if err := dfs(n); err != nil {
			return err
		}
	}
	return nil
}
