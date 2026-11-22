package agentcmd

import "errors"

const deploymentCorrelationVersion = "dill.agent-deployment-correlation/1"

var errDeploymentCorrelationBinding = errors.New("deployment correlation requires matching original journal and valid request IDs")

// deploymentCorrelation is a private projection, not an extension to the v1
// deployment status or journal recovery output. RequestID is caller-provided;
// OperationID is Dill's independent mutation operation ID, when recorded.
// ManifestEvidence describes the commitment, not the outcome: recorded means
// retained; compacted means the row survives without its snapshot metadata;
// missing means no matching row was found, not proof of non-admission. Legacy
// means the original encoding was v1, pending means no outcome was recorded,
// and not-applicable covers fence and removal rows. Only recorded carries a digest.
type deploymentCorrelation struct {
	Version          string `json:"version"`
	JournalID        string `json:"journalId"`
	RequestID        string `json:"requestId"`
	Mode             string `json:"mode,omitempty"`
	RecordedState    string `json:"recordedState,omitempty"`
	State            string `json:"state"`
	Effects          string `json:"effects"`
	OperationID      string `json:"operationId,omitempty"`
	ManifestEvidence string `json:"manifestEvidence"`
	ManifestSHA256   string `json:"manifestSha256,omitempty"`
}

// projectDeploymentCorrelation requires the validated, unmodified original
// journal, not an arbitrary decoded history, live status, or a reopened and
// upgraded journal. The caller must independently establish its target/source
// association; matching a journal ID does not authenticate it. In particular,
// v2 alone cannot prove original journal-bound submission, migration origin, or
// human approval. This function does not validate the entire journal or read it.
func projectDeploymentCorrelation(original deploymentHistoryFile, expectedJournalID, requestID string) (deploymentCorrelation, error) {
	if !validApplyID(expectedJournalID) || original.Journal != expectedJournalID || !validApplyID(requestID) {
		return deploymentCorrelation{}, errDeploymentCorrelationBinding
	}
	if original.Version != 1 && original.Version != 2 {
		return deploymentCorrelation{}, errJournalStatusInvalid
	}
	result := deploymentCorrelation{
		Version: deploymentCorrelationVersion, JournalID: original.Journal, RequestID: requestID,
		State: "missing", Effects: "unknown", ManifestEvidence: "missing",
	}
	for _, row := range original.Rows {
		if row.RequestID != requestID {
			continue
		}
		compacted := original.Version == 2 && validCompactRecord(row)
		if !compacted && !validDeploymentRecord(row) {
			return deploymentCorrelation{}, errJournalStatusInvalid
		}
		result.Mode, result.RecordedState = row.Mode, row.State
		result.State, result.Effects, result.OperationID = row.State, row.Effects, row.OperationID
		if row.State == "pending" {
			result.State = "unknown" // Offline, durable intent is not an observed operation or outcome.
		}
		switch {
		case row.Mode != "create" && row.Mode != "update":
			result.ManifestEvidence = "not-applicable"
		case original.Version == 1:
			result.ManifestEvidence = "legacy"
		case row.State == "pending":
			result.ManifestEvidence = "pending"
		case compacted:
			result.ManifestEvidence = "compacted"
		default:
			// Copy the commitment to the exact reviewed catalog manifest bytes.
			// It says nothing about effects or whether the stage is still usable.
			result.ManifestEvidence, result.ManifestSHA256 = "recorded", row.Manifest
		}
		break
	}
	return result, nil
}
