package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestClassifyProvenance(t *testing.T) {
	opID := "op_" + strings.Repeat("a", 32)
	digest := "sha256:" + strings.Repeat("b", 64)
	pair := func(op, snap string) map[string]string {
		return map[string]string{ProvenanceOperationIDLabel: op, ProvenanceSnapshotDigestLabel: snap}
	}
	for _, tc := range []struct {
		name       string
		live       *LiveConfig
		op, digest string
		want       ProvenanceEvidence
	}{
		{"exact pair", &LiveConfig{SystemLabels: pair(opID, digest)}, opID, digest, ProvenanceCorroboratedMetadata},
		{"no observation", nil, opID, digest, ProvenanceInconclusive},
		{"no labels", &LiveConfig{}, opID, digest, ProvenanceInconclusive},
		{"operation absent", &LiveConfig{SystemLabels: map[string]string{ProvenanceSnapshotDigestLabel: digest}}, opID, digest, ProvenanceInconclusive},
		{"snapshot absent", &LiveConfig{SystemLabels: map[string]string{ProvenanceOperationIDLabel: opID}}, opID, digest, ProvenanceInconclusive},
		{"operation mismatch", &LiveConfig{SystemLabels: pair("op_"+strings.Repeat("c", 32), digest)}, opID, digest, ProvenanceInconclusive},
		{"snapshot mismatch", &LiveConfig{SystemLabels: pair(opID, "sha256:"+strings.Repeat("c", 64))}, opID, digest, ProvenanceInconclusive},
		{"user labels only", &LiveConfig{UserLabels: pair(opID, digest)}, opID, digest, ProvenanceInconclusive},
		{"empty pair is not evidence", &LiveConfig{SystemLabels: pair("", "")}, "", "", ProvenanceInconclusive},
		{"noncanonical operation ID", &LiveConfig{SystemLabels: pair(strings.ToUpper(opID), digest)}, strings.ToUpper(opID), digest, ProvenanceInconclusive},
		{"bare digest is not label encoding", &LiveConfig{SystemLabels: pair(opID, digest[7:])}, opID, digest[7:], ProvenanceInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyProvenance(tc.live, tc.op, tc.digest); got != tc.want {
				t.Fatalf("ClassifyProvenance = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContainerCreateRejectsProvenanceServiceLabels(t *testing.T) {
	// Zero-value adapters have no usable SDK connection: the existing guard
	// must reject these even if config validation was skipped.
	for name, engine := range map[string]Engine{"docker": &DockerEngine{}, "podman": &PodmanEngine{}} {
		for _, label := range []string{ProvenanceOperationIDLabel, ProvenanceSnapshotDigestLabel, provenanceLabelPrefix + "future"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				svc := &config.Service{Image: "busybox", Labels: map[string]string{label: "override"}}
				if id, err := engine.StartService(context.Background(), "app", svc, "stack"); id != "" || err == nil {
					t.Fatalf("StartService = %q, %v; want label rejection", id, err)
				}
			})
		}
	}
}
