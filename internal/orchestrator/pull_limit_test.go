package orchestrator

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestQueuedPullStopsBeforeEngineAccess(t *testing.T) {
	if !pullSlots.TryAcquire(2) {
		t.Fatal("expected both pull slots available")
	}
	defer pullSlots.Release(2)
	for _, e := range []Engine{new(DockerEngine), new(PodmanEngine)} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		err := e.PullImage(ctx, "synthetic:tag", io.Discard)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%T: expected queued pull timeout, got %v", e, err)
		}
	}
}
