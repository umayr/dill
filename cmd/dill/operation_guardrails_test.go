package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestOperationUpGuardrailComposition(t *testing.T) {
	for _, flags := range [][]string{{"--no-prune"}, {"--alpha-disposable", "--no-prune=false"}, {"--alpha-disposable", "--no-deps", "app"}} {
		t.Run(strings.Join(flags, "/"), func(t *testing.T) {
			e := newDisposableUpEngine()
			withTestRuntime(t, e, disposableConfig(), func() {
				result, code, stderr := captureOperation(t, "", operationArgs("up", "docker", append([]string{"-p", "test"}, flags...)...))
				if code != 0 || result.Outcome != "succeeded" || result.Effects != "complete" || stderr != "" {
					t.Fatalf("result=%+v code=%d stderr=%q", result, code, stderr)
				}
				for _, event := range e.events {
					if strings.HasPrefix(event, "remove:") || strings.HasPrefix(event, "stop:") {
						t.Fatalf("orphan pruned: %v", e.events)
					}
				}
			})
		})
	}
}

func TestOperationDisposableForeignResourceHasNoEffects(t *testing.T) {
	for _, resource := range []string{"volume", "container"} {
		t.Run(resource, func(t *testing.T) {
			e := newDisposableUpEngine()
			if resource == "volume" {
				e.facts[1].Exists = true
				e.facts[1].Settings = json.RawMessage(`{"Driver":"local","Labels":{"dill.managed":"true","dill.stack":"foreign"}}`)
			} else {
				e.live["test_app"] = &orchestrator.LiveConfig{ID: "FOREIGN_SENTINEL"}
			}
			withTestRuntime(t, e, disposableConfig(), func() {
				result, code, stderr := captureOperation(t, "", operationArgs("up", "docker", "-p", "test", "--alpha-disposable"))
				if code != 1 || result.Effects != "none" || stderr != "" || len(e.events) != 0 {
					t.Fatalf("result=%+v code=%d stderr=%q mutations=%v", result, code, stderr, e.events)
				}
			})
		})
	}
}

type noEnforcerEngine struct{ *preMutationEngine }

func (e *noEnforcerEngine) DisposableImage(context.Context, string) (string, []string, error) {
	return "prepared-image", nil, nil
}
func (e *noEnforcerEngine) PreviewResources(context.Context, string, []string) ([]orchestrator.PreviewResource, error) {
	return []orchestrator.PreviewResource{{Kind: "network", Name: "test"}, {Kind: "volume", Name: "test_data"}}, nil
}
func TestOperationDisposableMissingEnforcerFailsClosed(t *testing.T) {
	e := &noEnforcerEngine{newDisposableUpEngine().preMutationEngine}
	withTestRuntime(t, e, disposableConfig(), func() {
		result, code, stderr := captureOperation(t, "", operationArgs("up", "docker", "-p", "test", "--alpha-disposable"))
		if code != 1 || result.Effects != "none" || stderr != "" || len(e.events) != 0 {
			t.Fatalf("result=%+v code=%d stderr=%q mutations=%v", result, code, stderr, e.events)
		}
	})
}

func TestOperationGuardrailFlagsAreUpOnlyBeforeAccess(t *testing.T) {
	withTestRuntime(t, newFakeEngine(), testConfig(), func() {
		configLoader = func(context.Context, string) (*config.DillConfig, error) {
			t.Fatal("loaded unsupported guardrail invocation")
			return nil, nil
		}
		for _, flag := range []string{"--no-prune", "--no-prune=false", "--alpha-disposable"} {
			result, code, stderr := captureOperation(t, "", operationArgs("rm", "docker", flag))
			if code != 2 || result.Effects != "none" || stderr != "" {
				t.Fatalf("result=%+v code=%d stderr=%q", result, code, stderr)
			}
		}
	})
}
