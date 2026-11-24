package dag

import (
	"context"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"io"
	"testing"
	"time"
)

type gateEngine struct {
	lifecycleEngine
	app, checking, release chan struct{}
	result                 orchestrator.JobResult
}

func (e *gateEngine) StartService(_ context.Context, n string, _ *config.Service, _ string) (string, error) {
	if n == "app" {
		close(e.app)
	}
	return n, nil
}
func (e *gateEngine) IsReady(ctx context.Context, id string, _ bool) (bool, error) {
	if id == "dep" {
		close(e.checking)
		select {
		case <-e.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return true, nil
}
func (e *gateEngine) WaitJob(ctx context.Context, _ string) (orchestrator.JobResult, error) {
	close(e.checking)
	select {
	case <-e.release:
		return e.result, nil
	case <-ctx.Done():
		return orchestrator.JobResult{}, ctx.Err()
	}
}
func TestDependencyGates(t *testing.T) {
	for _, tc := range []struct {
		condition config.DependencyCondition
		result    orchestrator.JobResult
		ok        bool
	}{{config.Started, orchestrator.JobResult{}, true}, {config.Healthy, orchestrator.JobResult{}, true}, {config.CompletedSuccessfully, orchestrator.JobResult{Known: true}, true}, {config.CompletedSuccessfully, orchestrator.JobResult{Known: true, ExitCode: 3}, false}, {config.CompletedSuccessfully, orchestrator.JobResult{}, false}} {
		t.Run(string(tc.condition), func(t *testing.T) {
			e := &gateEngine{app: make(chan struct{}), checking: make(chan struct{}), release: make(chan struct{}), result: tc.result}
			g, err := Build(map[string]*config.Service{"dep": {PullPolicy: "never", HealthCheck: &config.HealthCheck{}}, "app": {PullPolicy: "never", DependsOn: []string{"dep"}, Conditions: map[string]config.DependencyCondition{"dep": tc.condition}}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- g.Run(context.Background(), e, "s", 2*time.Second, nil, nil, func(string) PullSink { return discardSink{io.Discard} })
			}()
			select {
			case <-e.checking:
			case <-time.After(time.Second):
				t.Fatal("no observation")
			}
			if tc.condition == config.Started {
				select {
				case <-e.app:
				case <-time.After(time.Second):
					t.Fatal("started waited for health")
				}
			} else {
				select {
				case <-e.app:
					t.Fatal("dependent started early")
				default:
				}
			}
			close(e.release)
			if err := <-done; (err == nil) != tc.ok {
				t.Fatalf("unexpected outcome: %v", err)
			}
			select {
			case <-e.app:
				if !tc.ok {
					t.Fatal("dependent ran after failure")
				}
			default:
				if tc.ok {
					t.Fatal("dependent did not run")
				}
			}
		})
	}
}
