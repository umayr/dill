//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCancellationAndCleanupFailureAreDistinguishable(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("private path sentinel")
	var sawStage bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := receive(ctx, root, Snapshot{"a", []Entry{entry("a", "secret")}}, MaximumLimits(), func(int, string, int, string) error { cancel(); return ctx.Err() }, func(items []created) error {
		sawStage = len(items) >= 2
		// Still clean the test artifacts, then simulate the error from cleanup.
		if e := cleanup(items); e != nil {
			t.Fatal(e)
		}
		return cleanupFailure
	}, unix.Fstatat, identity)
	var ce *CleanupError
	if !sawStage || !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupFailure) || !errors.As(err, &ce) {
		t.Fatalf("missing structured failure: %v", err)
	}
}
