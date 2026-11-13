package main

import (
	"context"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestOperationDestructiveCaptureVisibleInDryAndDeclinedReceipt(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		for _, name := range []string{"rm", "down", "teardown"} {
			for _, dry := range []bool{true, false} {
				if name == "down" && !dry {
					continue
				} // down deliberately does not prompt
				t.Run(engine+"/"+name+map[bool]string{true: "/dry", false: "/declined"}[dry], func(t *testing.T) {
					f := &failingLifecycleEngine{fakeEngine: newFakeEngine()}
					f.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
					cfg := testConfig()
					cfg.Engine = engine
					withTestRuntime(t, f, cfg, func() {
						args := operationArgs(name, engine)
						if dry {
							args = append(args, "--dry-run")
						}
						result, code, stderr := captureOperation(t, "n\n", args)
						wantOutcome := "cancelled"
						if dry {
							wantOutcome = "succeeded"
						}
						if code != 0 || result.Outcome != wantOutcome || result.Effects != "none" || len(f.calls) != 0 {
							t.Fatalf("result=%+v code=%d mutations=%v", result, code, f.calls)
						}
						action := "intent.remove"
						if name == "rm" {
							action = "intent.removeStoppedCandidate"
						}
						captured := 0
						for _, resource := range result.Resources {
							if resource.Kind == "container" && resource.IdentityDigest == operation.ResourceDigest("container", "app_web") && resource.Action == action && resource.Effect == "none" {
								captured++
							}
						}
						if captured != 1 {
							t.Fatalf("captured identity absent/duplicated: %+v", result.Resources)
						}
						if name == "teardown" {
							foundImage := false
							for _, resource := range result.Resources {
								if resource.Kind == "image" && resource.IdentityDigest == operation.ResourceDigest("image", "busybox-id") && resource.Action == "intent.remove" {
									foundImage = true
								}
							}
							if !foundImage || len(result.Resources) != 4 {
								t.Fatalf("incomplete teardown capture: %+v", result.Resources)
							}
						}
						if dry && stderr != "" {
							t.Fatalf("dry prompt: %q", stderr)
						}
						if !dry && (!strings.Contains(stderr, "resource intents; sha256:") || !strings.Contains(stderr, "inventoryComplete=true") || strings.Contains(stderr, "app_web") || strings.Contains(stderr, "busybox")) {
							t.Fatalf("unsafe/missing intent prompt: %q", stderr)
						}
					})
				})
			}
		}
	}
}

type operationConfirmationReuseEngine struct{ *reuseDuringConfirmationEngine }

func (e *operationConfirmationReuseEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	live, err := e.fakeEngine.InspectConfig(ctx, name)
	if err != nil {
		return live, err
	}
	e.inspects++
	// Common ownership census, handler-owned capture, then mutation recheck.
	if e.inspects >= 3 {
		live.ID = "replacement-id"
	}
	return live, nil
}
func TestOperationRmReceiptDoesNotAuthorizeReplacementAfterPrompt(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		e := &operationConfirmationReuseEngine{&reuseDuringConfirmationEngine{fakeEngine: newFakeEngine()}}
		e.statuses["app_web"] = &orchestrator.ContainerStatus{Name: "app_web", State: "exited"}
		cfg := testConfig()
		cfg.Engine = engine
		withTestRuntime(t, e, cfg, func() {
			result, code, stderr := captureOperation(t, "y\n", operationArgs("rm", engine))
			if code != 1 || result.Effects != "none" || len(e.mutations) != 0 || !strings.Contains(stderr, "sha256:") {
				t.Fatalf("result=%+v mutations=%v prompt=%q", result, e.mutations, stderr)
			}
			for _, resource := range result.Resources {
				if resource.IdentityDigest == operation.ResourceDigest("container", "replacement-id") {
					t.Fatal("replacement authorized by original prompt")
				}
			}
		})
	}
}
