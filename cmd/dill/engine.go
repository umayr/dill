package main

import (
	"context"
	"os"

	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
)

func newEngine(ctx context.Context, engineName string) (orchestrator.Engine, error) {
	return engineFactory(ctx, engineName)
}

var engineFactory = defaultEngineFactory

func defaultEngineFactory(ctx context.Context, engineName string) (orchestrator.Engine, error) {
	switch engineName {
	case "docker":
		return orchestrator.NewDockerEngine()
	case "podman", "":
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
	switch e.(type) {
	case *orchestrator.DockerEngine:
		return "docker"
	default:
		return "podman"
	}
}
