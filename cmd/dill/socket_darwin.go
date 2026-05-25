package main

import (
	"os/exec"
	"strings"
)

// podmanSocket returns the Podman socket URI for macOS.
// It asks the running Podman machine for its socket path via `podman machine inspect`.
// If that fails (machine not running, podman not installed), returns an empty string
// which will cause NewPodmanEngine to fail with a descriptive error.
func podmanSocket() string {
	out, err := exec.Command("podman", "machine", "inspect",
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
