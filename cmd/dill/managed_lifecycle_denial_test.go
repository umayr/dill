package main

import (
	"context"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"strings"
	"testing"
)

func TestManagedLifecycleCannotUseCreationExpectation(t *testing.T) {
	f := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	for _, action := range []string{"start", "stop", "restart"} {
		args := namedPreviewArgs(f, action)
		args[2] = operation.Version
		args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", "sha256:"+strings.Repeat("a", 64))
		_, _, err := captureCommand(t, `{"mode":"create","resources":[]}`, func() error { return runManagedOperation(context.Background(), args) })
		if err == nil {
			t.Fatal("lifecycle creation accepted", action)
		}
		f.mu.Lock()
		writes := f.writes
		f.mu.Unlock()
		if writes != 0 {
			t.Fatal("creation denial attempted engine write", action)
		}
	}
}
