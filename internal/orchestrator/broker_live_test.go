package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"testing"
)

func TestBrokerLabelsCannotAuthorizeOldOrForgedSecrets(t *testing.T) {
	m := &brokersecret.Materialized{}
	for _, labels := range []map[string]string{{SecretEnabledLabel: "true"}, {SecretEnabledLabel: "false"}, {BrokerRegistrationLabel: "forged"}, {SecretEnabledLabel: "true", BrokerRegistrationLabel: "forged", BrokerGenerationLabel: "forged", BrokerHandleLabel: "forged"}} {
		if err := CheckBrokerLive(&LiveConfig{SystemLabels: labels}, m); err == nil {
			t.Fatal("forged/retired protection adopted")
		}
	}
}
func TestBrokerLabelsAreInternalOnly(t *testing.T) {
	labels := map[string]string{}
	applyBrokerLabels(context.Background(), labels)
	if len(labels) != 0 {
		t.Fatal("ordinary create acquired broker labels")
	}
}
