package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/preflight"
)

func TestNamedProducerCommitmentsBindSnapshotAndForce(t *testing.T) {
	cfg := testConfig()
	cfg.Services["web"].PullPolicy = "never"
	n, e := namedProducerFixture(true)
	if err := n.complete(context.Background(), e, cfg); err != nil {
		t.Fatal(err)
	}
	inv := preflight.Invocation{Operation: "up", Stack: "app", Timeout: time.Minute, Source: "cli"}
	target := preflight.Target{Engine: "docker", EndpointDigest: "sha256:" + strings.Repeat("c", 64), QualifiedIdentityDigest: "sha256:" + strings.Repeat("d", 64)}
	capture, base, err := produceNamedCapture(context.Background(), inv, cfg, n, target, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := base.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if capture.upPlan == nil || capture.upPlan.Changes[0].Kind != plan.KindNoop {
		t.Fatal("expected actual planner no-op")
	}
	inv.ForceRecreate = true
	forced, fs, err := produceNamedCapture(context.Background(), inv, cfg, n, target, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	fd, err := fs.Digest()
	if err != nil || fd == digest {
		t.Fatal("force not bound", err)
	}
	if forced.upPlan.Changes[0].Kind != plan.KindRecreate {
		t.Fatal("force was not applied to plan")
	}
	inv.ForceRecreate = false
	n.containers["app_web"].ID = strings.Repeat("f", 64)
	_, changed, err := produceNamedCapture(context.Background(), inv, cfg, n, target, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	cd, err := changed.Digest()
	if err != nil || cd == digest {
		t.Fatal("identity not bound", err)
	}
	// Restore the same actual ID, then change only the original desired
	// reference while retaining the separately prepared content ID.
	n.containers["app_web"].ID = strings.Repeat("a", 64)
	cfg.Services["web"].Image = "another-reference:same-content"
	_, changed, err = produceNamedCapture(context.Background(), inv, cfg, n, target, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	cd, err = changed.Digest()
	if err != nil || cd == digest {
		t.Fatal("desired reference provenance not bound", err)
	}
}
