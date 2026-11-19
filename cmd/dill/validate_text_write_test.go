package main

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestValidateTextReturnsWriteFailure(t *testing.T) {
	closed, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	withTestRuntime(t, newFakeEngine(), testConfig(), func() {
		stdout := os.Stdout
		os.Stdout = closed
		defer func() { os.Stdout = stdout }()
		err := runValidate(context.Background(), commandOptions{ConfigFile: "compose.pkl"}, nil)
		if !errors.Is(err, os.ErrClosed) || exitCodeFor(err) != 1 || failureKind(err) != "operational_failure" {
			t.Fatalf("want closed-writer operational failure, got err=%v exit=%d kind=%s", err, exitCodeFor(err), failureKind(err))
		}
	})
}
