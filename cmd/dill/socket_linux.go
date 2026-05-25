package main

import (
	"fmt"
	"os"
)

// podmanSocket returns the Podman socket URI for Linux.
// Uses $XDG_RUNTIME_DIR if set (standard for systemd user sessions),
// otherwise falls back to the conventional /run/user/<uid> path.
func podmanSocket() string {
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return "unix://" + xdg + "/podman/podman.sock"
	}
	return fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", os.Getuid())
}
