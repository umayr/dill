package main

import (
	"context"
	"fmt"
	"os"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

func newEngine(ctx context.Context, engineName string, opts commandOptions) (orchestrator.Engine, error) {
	endpoint, err := localEngineSocket(opts)
	if err != nil {
		return nil, err
	}
	if opts.operation != nil && opts.operation.engine != nil {
		if opts.operation.engine.Adapter != engineName || opts.operation.endpoint != endpoint {
			return nil, usageError{"operation target changed"}
		}
		return opts.operation.engine, nil
	}
	var engine orchestrator.Engine
	if opts.operation != nil && opts.operation.coordination != nil {
		coord := opts.operation.coordination
		if engineName != "docker" || coord.target == nil || coord.guard == nil {
			return nil, usageError{"qualified target is not initialized"}
		}
		engine, err = orchestrator.NewDockerEngineWithClient(coordination.Endpoint, coord.target.Client())
		if err == nil {
			coord.guard.Engine = engine
			engine = coord.guard
		}
	} else {
		engine, err = engineFactory(ctx, engineName, endpoint)
	}
	if err != nil {
		return nil, err
	}
	if opts.operation != nil {
		opts.operation.engine = &operation.Engine{Engine: engine, Recorder: opts.operation.recorder, Adapter: engineName, Borrowed: true}
		opts.operation.endpoint = endpoint
		return opts.operation.engine, nil
	}
	return engine, nil
}

var engineFactory = defaultEngineFactory

func defaultEngineFactory(ctx context.Context, engineName, endpoint string) (orchestrator.Engine, error) {
	switch engineName {
	case "docker":
		if endpoint != "" {
			return orchestrator.NewDockerEngineAt(endpoint)
		}
		return orchestrator.NewDockerEngine()
	case "podman", "":
		if endpoint != "" {
			return orchestrator.NewPodmanEngine(ctx, endpoint)
		}
		return newPodmanEngine(ctx)
	default:
		return nil, usageError{"unknown engine " + engineName + " (supported: podman, docker)"}
	}
}

func newPodmanEngine(ctx context.Context) (orchestrator.Engine, error) {
	socket := os.Getenv("PODMAN_SOCKET")
	if socket == "" {
		socket = podmanSocket()
	}
	logger.Debug("connecting to podman", "socket", socket)
	return orchestrator.NewPodmanEngine(ctx, socket)
}

// ownedServiceID resolves the configured name, verifies labels, and binds the
// subprocess to the inspected immutable ID rather than a reusable name.
func ownedServiceID(ctx context.Context, engine orchestrator.Engine, stack, service string, svc *config.Service) (string, error) {
	name := serviceContainerName(stack, service, svc)
	id, err := orchestrator.OwnedID(ctx, engine, name, stack, service)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("service %q: %w: %s", service, orchestrator.ErrNotFound, name)
	}
	return id, nil
}

// engineCommand pins subprocesses to the endpoint used by the SDK. Global
// flags precede the subcommand and override separately selected CLI contexts.
func engineCommand(e orchestrator.Engine, args ...string) (string, []string) {
	binary := engineBinary(e)
	if target, ok := e.(interface{ CLIEndpoint() string }); ok && target.CLIEndpoint() != "" {
		flag := "--url"
		if binary == "docker" {
			flag = "--host"
		}
		args = append([]string{flag, target.CLIEndpoint()}, args...)
	}
	return binary, args
}

func engineBinary(e orchestrator.Engine) string {
	if named, ok := e.(interface{ EngineName() string }); ok {
		return named.EngineName()
	}
	switch e.(type) {
	case *orchestrator.DockerEngine:
		return "docker"
	default:
		return "podman"
	}
}
