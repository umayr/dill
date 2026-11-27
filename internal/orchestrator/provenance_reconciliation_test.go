package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

type provenancePlanSource struct{ live *orchestrator.LiveConfig }

func (s provenancePlanSource) InspectConfig(context.Context, string) (*orchestrator.LiveConfig, error) {
	return s.live, nil
}
func (provenancePlanSource) LocalImageID(context.Context, string) (string, error) { return "", nil }
func (provenancePlanSource) ListStack(context.Context, string) ([]string, error)  { return nil, nil }

func TestProvenanceDoesNotChangeContainerPlanOrOwnership(t *testing.T) {
	ctx := context.Background()
	const stack, service = "stack", "app"
	svc := &config.Service{Image: "busybox", Labels: map[string]string{"example.role": "app"}}
	cfg := &config.DillConfig{Services: map[string]*config.Service{service: svc}}
	hash, err := config.ServiceConfigHash(stack, service, svc)
	if err != nil {
		t.Fatal(err)
	}
	live := &orchestrator.LiveConfig{
		ID: "synthetic-container-id", Image: svc.Image,
		UserLabels: map[string]string{"example.role": "app"},
		SystemLabels: map[string]string{
			"dill.managed": "true", "dill.stack": stack, "dill.service": service, "dill.config-hash": hash,
		},
	}
	source := provenancePlanSource{live}
	opID, snapshotDigest := "op_"+strings.Repeat("a", 32), "sha256:"+strings.Repeat("b", 64)
	for _, tc := range []struct{ name, op, snapshot string }{
		{"absent", "", ""},
		{"matching", opID, snapshotDigest},
		{"changed", "op_" + strings.Repeat("c", 32), "sha256:" + strings.Repeat("d", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.op != "" {
				live.SystemLabels[orchestrator.ProvenanceOperationIDLabel] = tc.op
				live.SystemLabels[orchestrator.ProvenanceSnapshotDigestLabel] = tc.snapshot
			}
			p, err := plan.Compute(ctx, cfg, source, stack)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Changes) != 1 || p.Changes[0].Kind != plan.KindNoop || len(p.Changes[0].Diffs) != 0 {
				t.Fatalf("live provenance changed plan against fixed config hash: %+v", p.Changes)
			}
			if got, err := config.ServiceConfigHash(stack, service, svc); err != nil || got != hash {
				t.Fatalf("desired hash changed: %q, %v", got, err)
			}
		})
	}

	live.SystemLabels[orchestrator.ProvenanceOperationIDLabel] = opID
	live.SystemLabels[orchestrator.ProvenanceSnapshotDigestLabel] = snapshotDigest
	live.SystemLabels["dill.stack"] = "foreign"
	if got := orchestrator.ClassifyProvenance(live, opID, snapshotDigest); got != orchestrator.ProvenanceCorroboratedMetadata {
		t.Fatalf("expected matching metadata despite foreign ownership, got %v", got)
	}
	if p, err := plan.Compute(ctx, cfg, source, stack); p != nil || !errors.Is(err, orchestrator.ErrOwnershipConflict) {
		t.Fatalf("matching metadata changed ownership decision: plan=%v, err=%v", p, err)
	}
}
