package dag

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type lifecycleEngine struct {
	orchestrator.Engine
	removed, stopped               []string
	stopErr, removeErr, networkErr error
	live                           map[string]*orchestrator.LiveConfig
}

func (e *lifecycleEngine) ListStack(context.Context, string) ([]string, error) {
	return []string{"a", "b", "foreign"}, nil
}
func (e *lifecycleEngine) InspectConfig(_ context.Context, n string) (*orchestrator.LiveConfig, error) {
	return e.live[n], nil
}
func (e *lifecycleEngine) StopService(_ context.Context, id string) error {
	e.stopped = append(e.stopped, id)
	return e.stopErr
}
func (e *lifecycleEngine) RemoveService(_ context.Context, id string) error {
	e.removed = append(e.removed, id)
	return e.removeErr
}
func (e *lifecycleEngine) RemoveNetwork(context.Context, string) error { return e.networkErr }
func (e *lifecycleEngine) EnsureNetwork(context.Context, string) error { return nil }
func (e *lifecycleEngine) StartService(context.Context, string, *config.Service, string) (string, error) {
	return "created-id", nil
}
func (e *lifecycleEngine) IsReady(context.Context, string, bool) (bool, error) {
	return false, errors.New("readiness failed")
}

type concurrentFailures struct {
	orchestrator.Engine
	ready         sync.WaitGroup
	first, second error
}

func (e *concurrentFailures) EnsureNetwork(context.Context, string) error { return nil }
func (e *concurrentFailures) StartService(_ context.Context, name string, _ *config.Service, _ string) (string, error) {
	e.ready.Done()
	e.ready.Wait()
	if name == "one" {
		return "", e.first
	}
	return "", e.second
}
func TestRunRetainsConcurrentErrors(t *testing.T) {
	e := &concurrentFailures{first: errors.New("first failure"), second: errors.New("second cleanup failure")}
	e.ready.Add(2)
	g, _ := Build(map[string]*config.Service{"one": {PullPolicy: "never"}, "two": {PullPolicy: "never"}})
	err := g.Run(context.Background(), e, "stack", time.Second, nil, nil, func(string) PullSink { return discardSink{io.Discard} })
	if !errors.Is(err, e.first) || !errors.Is(err, e.second) {
		t.Fatalf("lost concurrent error: %v", err)
	}
}

func TestDownAggregatesAndUsesInspectedIDs(t *testing.T) {
	failure := errors.New("injected")
	labels := map[string]string{"dill.managed": "true", "dill.stack": "stack", "dill.service": "svc"}
	e := &lifecycleEngine{stopErr: failure, removeErr: failure, networkErr: failure, live: map[string]*orchestrator.LiveConfig{"a": {ID: "id-a", SystemLabels: labels}, "b": {ID: "id-b", SystemLabels: labels}, "foreign": {ID: "foreign-id"}}}
	err := Down(context.Background(), e, "stack", nil)
	if !errors.Is(err, failure) || !errors.Is(err, orchestrator.ErrOwnershipConflict) {
		t.Fatalf("errors lost: %v", err)
	}
	for _, s := range []string{"id-a", "id-b", "network", "foreign"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("missing %s: %v", s, err)
		}
	}
	if strings.Join(e.removed, ",") != "id-a,id-b" {
		t.Fatalf("unsafe targets %v", e.removed)
	}
}
func TestRunReturnsOriginalAndCleanupErrors(t *testing.T) {
	failure := errors.New("cleanup failure")
	e := &lifecycleEngine{stopErr: failure, removeErr: failure}
	g, _ := Build(map[string]*config.Service{"a": {PullPolicy: "never"}})
	err := g.Run(context.Background(), e, "stack", time.Second, nil, nil, func(string) PullSink { return discardSink{io.Discard} })
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "readiness failed") || !strings.Contains(err.Error(), "created-id") {
		t.Fatalf("lost failure: %v", err)
	}
}
