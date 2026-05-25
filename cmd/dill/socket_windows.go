package main

// podmanSocket returns the Podman named pipe URI for Windows.
// Podman on Windows uses a named pipe via the default Podman machine.
// Override with the PODMAN_SOCKET environment variable if using a non-default machine.
func podmanSocket() string {
	return "npipe:////./pipe/podman-machine-default"
}
