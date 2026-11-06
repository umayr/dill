package dag

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type cancellationEngine struct {
	orchestrator.Engine
	goodReady       chan struct{}
	requestStarted  chan struct{}
	requestCanceled chan struct{}
	cleanupErrors   []error
	removed         []string
}

func (e *cancellationEngine) EnsureNetwork(context.Context, string) error { return nil }
func (e *cancellationEngine) StartService(ctx context.Context, name string, _ *config.Service, _ string) (string, error) {
	switch name {
	case "good":
		return "good-id", nil
	case "blocked":
		select {
		case <-e.goodReady:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		close(e.requestStarted)
		<-ctx.Done()
		close(e.requestCanceled)
		return "", ctx.Err()
	case "failure":
		select {
		case <-e.requestStarted:
			return "", errors.New("injected sibling failure")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	default:
		return "", errors.New("unexpected service")
	}
}
func (e *cancellationEngine) IsReady(context.Context, string, bool) (bool, error) {
	close(e.goodReady)
	return true, nil
}
func (e *cancellationEngine) StopService(ctx context.Context, _ string) error {
	e.checkCleanupContext(ctx)
	return ctx.Err()
}
func (e *cancellationEngine) RemoveService(ctx context.Context, id string) error {
	e.checkCleanupContext(ctx)
	e.removed = append(e.removed, id)
	return ctx.Err()
}
func (e *cancellationEngine) checkCleanupContext(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		e.cleanupErrors = append(e.cleanupErrors, err)
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Minute {
		e.cleanupErrors = append(e.cleanupErrors, errors.New("cleanup must have an independent, bounded deadline"))
	}
}

func TestRunCancellationAndIndependentRollback(t *testing.T) {
	for _, mode := range []string{"timeout", "parent cancellation", "sibling failure"} {
		t.Run(mode, func(t *testing.T) {
			engine := &cancellationEngine{goodReady: make(chan struct{}), requestStarted: make(chan struct{}), requestCanceled: make(chan struct{})}
			services := map[string]*config.Service{
				"good":    {Image: "image", PullPolicy: "never"},
				"blocked": {Image: "image", PullPolicy: "never"},
			}
			if mode == "sibling failure" {
				services["failure"] = &config.Service{Image: "image", PullPolicy: "never"}
			}
			g, err := Build(services)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 3 * time.Second
			if mode == "timeout" {
				timeout = 200 * time.Millisecond
			}
			if mode == "parent cancellation" {
				go func() {
					select {
					case <-engine.requestStarted:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			err = g.Run(ctx, engine, "test", timeout, nil, nil, func(string) PullSink { return discardSink{io.Discard} })
			if err == nil {
				t.Fatal("expected deployment failure")
			}
			select {
			case <-engine.requestCanceled:
			default:
				t.Fatal("blocked sibling was not cancelled")
			}
			if len(engine.cleanupErrors) != 0 {
				t.Fatalf("rollback contexts: %v", engine.cleanupErrors)
			}
			if len(engine.removed) != 1 || engine.removed[0] != "good-id" {
				t.Fatalf("rollback removed %v, want good-id", engine.removed)
			}
		})
	}
}
