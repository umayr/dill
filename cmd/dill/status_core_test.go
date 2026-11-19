package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

type statusCoreEngine struct{ orchestrator.Engine }

func (*statusCoreEngine) Close() error { return nil }
func (*statusCoreEngine) ListStack(context.Context, string) ([]string, error) {
	return []string{"a", "b"}, nil
}
func (*statusCoreEngine) ServiceStatus(_ context.Context, name string) (*orchestrator.ContainerStatus, error) {
	if name == "b" {
		return nil, errors.New("gone")
	}
	return &orchestrator.ContainerStatus{Name: name, State: "running"}, nil
}

type psInspectEngine struct {
	statusCoreEngine
	inspect func(string) (*orchestrator.ContainerStatus, error)
}

func (e *psInspectEngine) ServiceStatus(_ context.Context, name string) (*orchestrator.ContainerStatus, error) {
	return e.inspect(name)
}

func TestPsInterruptedInspection(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }

	for _, tc := range []struct {
		name string
		want error
	}{
		{"canceled context", context.Canceled},
		{"inspection deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls int
			engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
				return &psInspectEngine{inspect: func(string) (*orchestrator.ContainerStatus, error) {
					calls++
					if tc.want == context.Canceled {
						cancel()
						return nil, errors.New("interrupted transport")
					}
					return nil, fmt.Errorf("inspection: %w", context.DeadlineExceeded)
				}}, nil
			}
			out, _, err := captureCommand(t, "", func() error {
				return runPs(ctx, commandOptions{}, []string{"--format", "json"})
			})
			if !errors.Is(err, tc.want) || exitCodeFor(err) != 1 || out != "" || calls != 1 {
				t.Fatalf("expected %v and no snapshot after one inspection; got err=%v exit=%d output=%q calls=%d", tc.want, err, exitCodeFor(err), out, calls)
			}
		})
	}
}

func TestPsReportsOrdinaryInspectionError(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return &statusCoreEngine{}, nil }
	out, _, err := captureCommand(t, "", func() error { return run([]string{"ps", "--format", "json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got []orchestrator.ContainerStatus
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[0].State != "running" || got[1].Name != "b" || got[1].State != "error" || got[1].Status != "gone" {
		t.Fatalf("unexpected JSON: %s", out)
	}
}

type failOnceStatusWriter struct {
	strings.Builder
	writes, failAt int
	err            error
}

func (w *failOnceStatusWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.Builder.Write(p)
}

func TestStatusTextReturnsSummaryWriteFailure(t *testing.T) {
	out := core.StatusResult{Containers: []*core.ContainerStatus{{Name: "a", State: "error"}}}
	want := errors.New("synthetic summary write failure")
	for _, tc := range []struct {
		name   string
		failAt int
	}{{"summary", 1}, {"inspection count", 2}, {"separator", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			w := &failOnceStatusWriter{failAt: tc.failAt, err: want}
			err := renderStatusText(w, out)
			if !errors.Is(err, want) || w.writes != tc.failAt {
				t.Fatalf("wanted summary error and no further writes; got err=%v writes=%d, output=%q", err, w.writes, w.String())
			}
		})
	}
}

func TestStatusTextSeparatesInspectionErrors(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return &statusCoreEngine{}, nil }
	out, _, err := captureCommand(t, "", func() error { return run([]string{"status"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Containers: 1 running, 0 not running, 1 inspection failed\n") || !strings.Contains(out, "gone") {
		t.Fatalf("expected the summary to separate inspection failures and the row to retain its error, got:\n%s", out)
	}
}

func TestStatusCoreCLIJSONRegression(t *testing.T) {
	oldLoader, oldFactory := configLoader, engineFactory
	defer func() { configLoader, engineFactory = oldLoader, oldFactory }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return testConfig(), nil }
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) { return &statusCoreEngine{}, nil }
	out, _, err := captureCommand(t, "", func() error { return run([]string{"status", "--format", "json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Containers []orchestrator.ContainerStatus `json:"containers"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Containers) != 2 || got.Containers[0].State != "running" || got.Containers[1].State != "error" || got.Containers[1].Status != "gone" {
		t.Fatalf("unexpected JSON: %s", out)
	}
}
