package preflight

import (
	"errors"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestContainerLeafPreservesSecretMarkerClassification(t *testing.T) {
	v := syntheticLive()
	v.SystemLabels[orchestrator.SecretEnabledLabel] = "false"
	// Presence is the established classification boundary, not truthiness. A
	// secret failure must reach the caller before any identifying review bytes.
	v.InspectedName = "different"
	v.SystemLabels["dill.stack"] = "different"
	if _, err := ContainerDigest("synthetic", "web", "synthetic_web", v); !errors.Is(err, config.ErrSecretUnavailable) {
		t.Fatal("secret failure lost private classification", err)
	}
}
