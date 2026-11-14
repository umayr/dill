package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
)

type inventoryEvidence struct {
	orchestrator.Engine
	facts   []orchestrator.PreviewResource
	image   string
	volumes []string
}

func (e *inventoryEvidence) DisposableImage(context.Context, string) (string, []string, error) {
	return e.image, e.volumes, nil
}
func (e *inventoryEvidence) PreviewResources(context.Context, string, []string) ([]orchestrator.PreviewResource, error) {
	return e.facts, nil
}
func namedProducerFixture(present bool) (*namedInventory, *inventoryEvidence) {
	n := newNamedInventory("app", nil)
	if present {
		n.listed = []string{"app_web"}
		n.containers["app_web"] = &orchestrator.LiveConfig{ID: strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), Image: "busybox", InspectedName: "app_web", HostConfigKnown: true, MountsKnown: true, NetworksKnown: true, Networks: []string{"app"}, SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}
	} else {
		n.containers["app_web"] = nil
	}
	return n, &inventoryEvidence{image: "sha256:" + strings.Repeat("b", 64), facts: []orchestrator.PreviewResource{{Kind: "network", Name: "app"}}}
}
func TestNamedInventoryAndProducerFiveRoutes(t *testing.T) {
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		for _, present := range []bool{false, true} {
			t.Run(action+map[bool]string{true: "-present", false: "-absent"}[present], func(t *testing.T) {
				cfg := testConfig()
				cfg.Services["web"].PullPolicy = "never"
				n, e := namedProducerFixture(present)
				if err := n.complete(context.Background(), e, cfg); err != nil {
					t.Fatal(err)
				}
				inv := preflight.Invocation{Operation: action, Stack: "app", Timeout: time.Minute, Source: "cli"}
				target := preflight.Target{Engine: "docker", EndpointDigest: "sha256:" + strings.Repeat("c", 64), QualifiedIdentityDigest: "sha256:" + strings.Repeat("d", 64)}
				capture, selection, err := produceNamedCapture(context.Background(), inv, cfg, n, target, strings.Repeat("e", 64))
				if !present && (action == "start" || action == "restart") {
					if err == nil {
						t.Fatal("absent direct start accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if capture.compared {
					t.Fatal("producer incorrectly authorized capture")
				}
				if _, err = selection.Digest(); err != nil {
					t.Fatal(err)
				}
				if len(selection.Resources) != 2 || selection.Coverage.Containers.Count != 1 {
					t.Fatal("incomplete rows", selection.Coverage)
				}
				if present && capture.serviceIDs["web"] != strings.Repeat("a", 64) {
					t.Fatal("lost immutable ID")
				}
				if action == "down" && capture.removal == nil {
					t.Fatal("missing removal intent")
				}
			})
		}
	}
}
func TestNamedInventoryRejectsIncompleteAndCopiesSnapshot(t *testing.T) {
	cfg := testConfig()
	cfg.Services["web"].PullPolicy = "never"
	for _, mutate := range []func(*namedInventory, *inventoryEvidence){
		func(n *namedInventory, e *inventoryEvidence) { n.listed = nil },
		func(n *namedInventory, e *inventoryEvidence) { n.listed = append(n.listed, "app_web") },
		func(n *namedInventory, e *inventoryEvidence) { n.containers["app_web"].InspectedName = "other" },
		func(n *namedInventory, e *inventoryEvidence) { e.image = "short" },
		func(n *namedInventory, e *inventoryEvidence) { e.volumes = []string{"/hidden"} },
		func(n *namedInventory, e *inventoryEvidence) { e.facts = nil },
	} {
		n, e := namedProducerFixture(true)
		mutate(n, e)
		if err := n.complete(context.Background(), e, cfg); err == nil {
			t.Fatal("accepted incomplete evidence")
		}
	}
	n, e := namedProducerFixture(true)
	original := n.containers["app_web"]
	n.containers["app_web"] = cloneNamedLive(original)
	if err := n.complete(context.Background(), e, cfg); err != nil {
		t.Fatal(err)
	}
	original.SystemLabels["dill.stack"] = "foreign"
	if n.containers["app_web"].SystemLabels["dill.stack"] != "app" {
		t.Fatal("snapshot aliases raw evidence")
	}
	snap, err := n.snapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	live, err := snap.InspectConfig(context.Background(), "app_web")
	if err != nil {
		t.Fatal(err)
	}
	live.SystemLabels["dill.stack"] = "foreign"
	if n.containers["app_web"].SystemLabels["dill.stack"] != "app" {
		t.Fatal("snapshot leaked mutable evidence")
	}
	if _, err := snap.InspectConfig(context.Background(), "unseen"); err == nil {
		t.Fatal("unknown lookup accepted")
	}
}
