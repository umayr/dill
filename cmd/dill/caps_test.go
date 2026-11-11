package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/orchestrator"
)

func TestCapsCommandRegistration(t *testing.T) {
	oldFactory := engineFactory
	defer func() { engineFactory = oldFactory }()
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Fatal("static discovery must not open an engine")
		return nil, fmt.Errorf("unexpected engine access")
	}
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			out, _, err := captureCommand(t, "", func() error {
				return run([]string{cmdCaps, "--engine", engine})
			})
			if err != nil {
				t.Fatal(err)
			}
			var caps orchestrator.Capabilities
			if err := json.Unmarshal([]byte(out), &caps); err != nil || caps.Engine != engine {
				t.Fatalf("caps output = %q, error = %v", out, err)
			}
		})
	}
	if got := closestCommand("cap"); got != cmdCaps {
		t.Fatalf("suggestion = %q, want %q", got, cmdCaps)
	}
	_, _, err := captureCommand(t, "", func() error { return run([]string{cmdCaps, "extra"}) })
	if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), cmdCaps) {
		t.Fatalf("unexpected argument error: %v", err)
	}
	_, _, err = captureCommand(t, "", func() error { return run([]string{"capabilities"}) })
	if exitCodeFor(err) != 2 {
		t.Fatalf("unreleased long name unexpectedly accepted: %v", err)
	}
}
