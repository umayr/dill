package main

import (
	"context"
	"os/exec"
	"strings"
)

// podmanSocket returns the Podman socket URI for macOS.
// It asks the running Podman machine for its socket path via `podman machine inspect`.
// If that fails (machine not running, podman not installed), returns an empty string
// so the constructor may use an explicit CONTAINER_HOST or report an error.
func podmanSocket(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "podman", "machine", "inspect",
		"--format", "{{.ConnectionInfo.PodmanSocket.Path}}").Output()
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return ""
	}
	return "unix://" + path
}
