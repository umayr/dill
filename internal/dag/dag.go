package dag

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
)

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
// Returns an error if any depends_on reference is missing or a cycle is detected.
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

// Run starts all services in dependency order. Services with no shared
// dependencies start concurrently. Each goroutine waits for its dependencies
// to be UP before starting its own container.
//
// If any service fails to become ready within timeout, the entire deployment
// is aborted and all already-started containers are torn down.
func (g *Graph) Run(ctx context.Context, engine orchestrator.Engine, stackName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var mu sync.Mutex
	var started []string // names of containers that were successfully started

	eg, egCtx := errgroup.WithContext(ctx)

	for _, n := range g.nodes {
		n := n
		eg.Go(func() error {
			// Wait for all dependencies to be UP.
			for _, dep := range n.deps {
				select {
				case <-dep.done:
					logger.Debug("dependency ready", "service", n.name, "dep", dep.name)
				case <-egCtx.Done():
					return fmt.Errorf("service %q: context cancelled while waiting for dependency %q", n.name, dep.name)
				}
			}

			// Pull image according to pull_policy.
			if err := maybePull(egCtx, engine, n.svc); err != nil {
				return fmt.Errorf("service %q: %w", n.name, err)
			}

			// Start the container.
			_, err := engine.StartService(egCtx, n.name, n.svc, stackName)
			if err != nil {
				return fmt.Errorf("service %q: %w", n.name, err)
			}
			mu.Lock()
			started = append(started, n.name)
			mu.Unlock()

			// Poll until the container is ready.
			hasHealthCheck := n.svc.HealthCheck != nil
			containerName := n.svc.ContainerName
			if containerName == "" {
				containerName = stackName + "_" + n.name
			}
			for {
				ready, err := engine.IsReady(egCtx, containerName, hasHealthCheck)
				if err != nil {
					return fmt.Errorf("service %q readiness check: %w", n.name, err)
				}
				if ready {
					logger.Info("service ready", "service", n.name)
					close(n.done)
					return nil
				}
				select {
				case <-time.After(500 * time.Millisecond):
				case <-egCtx.Done():
					return fmt.Errorf("service %q did not become ready within timeout", n.name)
				}
			}
		})
	}

	if err := eg.Wait(); err != nil {
		logger.Warn("deployment failed, tearing down started services", "err", err)
		// Use a fresh context for teardown since the original may be cancelled.
		teardown(context.Background(), engine, started)
		return err
	}
	return nil
}

// Down queries the engine for all containers in the stack and stops + removes them.
// It does not require the config file — it relies solely on the dill.stack label.
func Down(ctx context.Context, engine orchestrator.Engine, stackName string) error {
	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack %q: %w", stackName, err)
	}
	if len(names) == 0 {
		logger.Info("no containers found for stack", "stack", stackName)
		return nil
	}

	logger.Info("tearing down stack", "stack", stackName, "containers", len(names))
	teardown(ctx, engine, names)
	return nil
}

// teardown stops and removes the named containers, logging but not returning errors.
func teardown(ctx context.Context, engine orchestrator.Engine, names []string) {
	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := engine.StopService(ctx, name); err != nil {
				logger.Debug("stop failed (may already be stopped)", "name", name, "err", err)
			}
			if err := engine.RemoveService(ctx, name); err != nil {
				logger.Warn("remove failed", "name", name, "err", err)
			}
		}()
	}
	wg.Wait()
}

// maybePull pulls the image based on the service's pull_policy.
func maybePull(ctx context.Context, engine orchestrator.Engine, svc *config.Service) error {
	switch svc.PullPolicy {
	case "always":
		return engine.PullImage(ctx, svc.Image)
	case "never":
		return nil
	default: // "missing" is the default
		exists, err := engine.ImageExists(ctx, svc.Image)
		if err != nil {
			return fmt.Errorf("checking image existence: %w", err)
		}
		if !exists {
			return engine.PullImage(ctx, svc.Image)
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
