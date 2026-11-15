package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedProducerRejectsAliasedPresentIDs(t *testing.T) {
	cfg := testConfig()
	cfg.Services["web"].PullPolicy = "never"
	cfg.Services["other"] = &config.Service{Image: "busybox", PullPolicy: "never"}
	inventory, evidence := namedProducerFixture(true)
	second := cloneNamedLive(inventory.containers["app_web"])
	second.InspectedName = "app_other"
	second.SystemLabels["dill.service"] = "other"
	inventory.containers["app_other"] = second
	inventory.listed = append(inventory.listed, "app_other")
	if err := inventory.complete(context.Background(), evidence, cfg); err != nil {
		t.Fatal(err)
	}
	inv := preflight.Invocation{Operation: "stop", Stack: "app", Timeout: time.Minute, Source: "cli"}
	target := preflight.Target{Engine: "docker", EndpointDigest: "sha256:" + strings.Repeat("c", 64), QualifiedIdentityDigest: "sha256:" + strings.Repeat("d", 64)}
	capture, selection, err := produceNamedCapture(context.Background(), inv, cfg, inventory, target, strings.Repeat("e", 64))
	if err == nil || capture != nil || len(selection.Resources) != 0 {
		t.Fatal("aliased IDs became usable review")
	}
}
