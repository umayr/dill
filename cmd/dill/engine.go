package main

import (
	"context"
	"os"

	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
)

func newEngine(ctx context.Context, engineName string) (orchestrator.Engine, error) {
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

func engineBinary(e orchestrator.Engine) string {
	switch e.(type) {
	case *orchestrator.DockerEngine:
		return "docker"
	default:
		return "podman"
	}
}
