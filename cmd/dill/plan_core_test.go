package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/umayr/dill/core"
	"github.com/umayr/dill/internal/plan"
)

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
