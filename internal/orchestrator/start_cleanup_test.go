package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartCreatedCleanup(t *testing.T) {
	startErr := errors.New("start failed")
	cleanupErr := errors.New("cleanup failed")
	for _, tc := range []struct {
		name                         string
		cancel, failCleanup, success bool
	}{
		{name: "start failure"}, {name: "cleanup failure", failCleanup: true}, {name: "canceled", cancel: true}, {name: "success", success: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			started, removed := false, false
			err := startCreated(ctx, ctx, "app", "created-id", func() error {
				started = true
				if tc.success {
					return nil
				}
				return startErr
			}, func(c context.Context, id string) error {
				removed = true
				if id != "created-id" || c.Err() != nil {
					t.Fatalf("cleanup id=%q context error=%v", id, c.Err())
				}
				deadline, ok := c.Deadline()
				if !ok || time.Until(deadline) <= 0 {
					t.Fatalf("cleanup deadline missing: %v", deadline)
				}
				if tc.failCleanup {
					return cleanupErr
				}
				return nil
			})
			if tc.success {
				if err != nil || removed || !started {
					t.Fatalf("err=%v started=%v removed=%v", err, started, removed)
				}
				return
			}
			want := startErr
			if tc.cancel {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !removed || started == tc.cancel {
				t.Fatalf("err=%v started=%v removed=%v", err, started, removed)
			}
			if tc.failCleanup && !errors.Is(err, cleanupErr) {
				t.Fatalf("missing cleanup error: %v", err)
			}
		})
	}
}
