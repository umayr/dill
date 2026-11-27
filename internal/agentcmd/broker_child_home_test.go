package agentcmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/brokersecret"
)

func TestBrokerChildHasPrivateHomeWithoutAmbientAuthority(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "environment")
	binary := filepath.Join(dir, "child")
	script := `#!/bin/sh
test -z "$BRINE_VAR_FORBIDDEN" && test -z "$CONTAINERS_CONF" || exit 1
printf '%s\n' "$HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" > 'CAPTURE_FILE'
printf '%s\n' '{"contract":"dill.broker-secrets/1"}'
`
	script = strings.ReplaceAll(script, "CAPTURE_FILE", capture)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "/operator/home")
	t.Setenv("CONTAINERS_CONF", "/operator/engine")
	t.Setenv("BRINE_VAR_FORBIDDEN", "not-authorized")
	cli := brokerCLI{exe: binary, pkl: "/operator/pkl"}
	if _, err := cli.call(context.Background(), "broker-preflight", filepath.Join(dir, "compose.pkl"), brokersecret.Scope{}, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || lines[0] == "/operator/home" || lines[1] != filepath.Join(lines[0], "config") || lines[2] != filepath.Join(lines[0], "cache") {
		t.Fatal("private child environment not installed")
	}
	if _, err = os.Stat(lines[0]); !os.IsNotExist(err) {
		t.Fatal("private child home retained after completion")
	}
}
