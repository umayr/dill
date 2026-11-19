package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

func TestPlanDiffTextReturnsWriteFailure(t *testing.T) {
	closed, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	clear(cfg.Services) // A valid empty stack has no drift; only the summary is rendered.
	withTestRuntime(t, newFakeEngine(), cfg, func() {
		for _, command := range []string{"plan", "diff"} {
			t.Run(command, func(t *testing.T) {
				stdout := os.Stdout
				os.Stdout = closed
				defer func() { os.Stdout = stdout }()
				err := run([]string{command})
				if !errors.Is(err, os.ErrClosed) || exitCodeFor(err) != 1 {
					t.Fatalf("want closed-writer failure and exit 1, got err=%v exit=%d", err, exitCodeFor(err))
				}
			})
		}
	})
}

func TestPlanTextAlignsLongFieldNames(t *testing.T) {
	cfg := testConfig()
	cfg.Services["web"].Environment = map[string]string{
		"MODE":                  "short-value",
		"APPLICATION_LOG_LEVEL": "long-value",
	}
	engine := newFakeEngine()
	engine.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "running"}
	withTestRuntime(t, engine, cfg, func() {
		out, _, err := captureCommand(t, "", func() error { return run([]string{"plan"}) })
		if !errors.Is(err, errPlanHasChanges) {
			t.Fatalf("expected plan drift, got %v", err)
		}
		columns := make(map[string]int)
		for _, line := range strings.Split(out, "\n") {
			for field, value := range map[string]string{
				"env.MODE": "short-value", "env.APPLICATION_LOG_LEVEL": "long-value",
			} {
				if strings.Contains(line, field+" ") && strings.HasSuffix(line, "[unset] → "+value) {
					columns[field] = strings.Index(line, "[unset]")
				}
			}
		}
		if len(columns) != 2 || columns["env.MODE"] != columns["env.APPLICATION_LOG_LEVEL"] {
			t.Fatalf("expected both fields and aligned values, got columns %v:\n%s", columns, out)
		}
	})
}

func TestPlanDiffTextKeepsMultilineValuesOnOneLine(t *testing.T) {
	cfg := testConfig()
	cfg.Services["web"].Environment = map[string]string{"MESSAGE": "first line\nsecond line"}
	engine := newFakeEngine()
	engine.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "running"}
	withTestRuntime(t, engine, cfg, func() {
		for _, tc := range []struct {
			command string
			exit    int
		}{{"plan", 1}, {"diff", 0}} {
			t.Run(tc.command, func(t *testing.T) {
				out, _, err := captureCommand(t, "", func() error { return run([]string{tc.command}) })
				if exitCodeFor(err) != tc.exit {
					t.Fatalf("got exit %d (%v), want %d", exitCodeFor(err), err, tc.exit)
				}
				if !strings.Contains(out, `"first line\nsecond line"`) || strings.Contains(out, "\nsecond line") ||
					!strings.Contains(out, "Plan: 0 to create, 1 to recreate, 0 to remove, 0 unchanged.\n") {
					t.Fatalf("expected a single-line environment diff and one recreation, got:\n%s", out)
				}
			})
		}
	})
}

func TestPlanCoreCLIParity(t *testing.T) {
	cfg := testConfig()
	engine := newFakeEngine()
	expected, err := core.ComputePlan(context.Background(), cfg, engine, "app")
	if err != nil {
		t.Fatal(err)
	}
	withTestRuntime(t, engine, cfg, func() {
		for _, name := range []string{"plan", "diff"} {
			out, _, err := captureCommand(t, "", func() error {
				return runPlanNamed(context.Background(), commandOptions{}, []string{"--format", "json"}, name)
			})
			if !errors.Is(err, errPlanHasChanges) {
				t.Fatalf("%s drift: %v", name, err)
			}
			// Compare the shared JSON representation.
			var actual, want any
			if err := json.Unmarshal([]byte(out), &actual); err != nil {
				t.Fatal(err)
			}
			var rendered bytes.Buffer
			if err := plan.RenderJSON(expected, &rendered); err != nil {
				t.Fatal(err)
			}
			b := rendered.Bytes()
			if err := json.Unmarshal(b, &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("%s: got %s want %s", name, out, b)
			}
		}
	})
}
