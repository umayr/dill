package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

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
