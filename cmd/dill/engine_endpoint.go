package main

import (
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/umayr/dill/internal/orchestrator"
)

// localEngineSocket selects only the new explicit local override. An empty
// result means legacy adapter selection, not a guessed or authenticated target.
// Do not resolve paths against the config checkout or probe the filesystem here.
func localEngineSocket(opts commandOptions) (string, error) {
	value := opts.EngineSocket
	selected := opts.EngineSocketSet || value != ""
	if !selected {
		value, selected = os.LookupEnv("DILL_ENGINE_SOCKET")
	}
	if !selected {
		return "", nil
	}
	invalid := usageError{"invalid engine socket: expected an absolute local path or unix:///path without authority, credentials, escapes, query or fragment"}
	if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) || strings.ContainsAny(value, "?#%\\") {
		return "", invalid
	}
	path := value
	if !strings.HasPrefix(value, "/") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", invalid
		}
		path = u.Path
		// Require the documented spelling, not unix:/path or an authority URI.
		if value != "unix://"+path {
			return "", invalid
		}
	}
	// Docker CLI trims host whitespace; never let it select a different socket.
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.TrimRightFunc(path, unicode.IsSpace) != path {
		return "", invalid
	}
	return "unix://" + path, nil
}

// engineEnvironment removes ambient target/TLS selectors for local CLI calls.
// Keep unrelated environment (including registry configuration) unchanged. For
// legacy remote endpoints preserve the existing environment and TLS behavior.
func engineEnvironment(engine orchestrator.Engine) []string {
	target, ok := engine.(interface{ CLIEndpoint() string })
	if !ok || !strings.HasPrefix(target.CLIEndpoint(), "unix://") {
		return os.Environ()
	}
	out := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION",
			"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "PODMAN_SOCKET":
			continue
		}
		out = append(out, entry)
	}
	return out
}
