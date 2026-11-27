package plan

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"testing"
)

func TestBrokerPlanCannotUseZeroEvidence(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker"}
	for _, m := range []*brokersecret.Materialized{nil, {}} {
		if _, err := ComputeBroker(context.Background(), cfg, nil, "app", m); err == nil {
			t.Fatal("zero evidence plan accepted")
		}
	}
}
