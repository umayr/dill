package orchestrator

import (
	"regexp"

	"github.com/umayr/dill/internal/config"
)

// These container annotation keys are reserved by the existing dill.* label
// boundary. They are not ownership labels and are not stamped by this package.
const (
	provenanceLabelPrefix         = config.SystemLabelPrefix + "provenance."
	ProvenanceOperationIDLabel    = provenanceLabelPrefix + "operation-id"
	ProvenanceSnapshotDigestLabel = provenanceLabelPrefix + "snapshot-digest"
)

var (
	provenanceOperationIDPattern    = regexp.MustCompile(`^op_[0-9a-f]{32}$`)
	provenanceSnapshotDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ProvenanceEvidence describes only the agreement of the observed annotations
// with the expected pair, not who wrote them or what the operation did.
type ProvenanceEvidence uint8

const (
	ProvenanceInconclusive ProvenanceEvidence = iota
	ProvenanceCorroboratedMetadata
)

// ClassifyProvenance compares container system labels with caller-supplied
// expectations: the durable apply operation ID and the verified snapshot manifest
// SHA-256. The label's sha256:<hex> and the receipt's bare hex encode the same
// digest, not independent commitments. This function does not verify the
// expectation's source. Labels can be spoofed; even a match is not proof of
// ownership, identity, success, or authorization to mutate or replay.
func ClassifyProvenance(live *LiveConfig, operationID, snapshotDigest string) ProvenanceEvidence {
	if live != nil &&
		provenanceOperationIDPattern.MatchString(operationID) &&
		provenanceSnapshotDigestPattern.MatchString(snapshotDigest) &&
		live.SystemLabels[ProvenanceOperationIDLabel] == operationID &&
		live.SystemLabels[ProvenanceSnapshotDigestLabel] == snapshotDigest {
		return ProvenanceCorroboratedMetadata
	}
	return ProvenanceInconclusive
}
