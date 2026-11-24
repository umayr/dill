package agentcmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
)

const journalRecoveryUsage = "usage: dill agent journal-recovery --state-dir /absolute/private/operations --journal-id ID --request-id ID"

var (
	errJournalRecoveryUnbound  = errors.New("deployment journal has no recorded identity; cannot bind recovery evidence")
	errJournalRecoveryMismatch = errors.New("deployment journal does not match the original journal ID")
	errJournalRecoveryOutput   = errors.New("cannot write journal recovery output")
)

// Only the selected request's outcome and its binding identifiers are emitted.
// Do not serialize journal records or include snapshot or ownership payloads.
type journalRecoveryEvidence struct {
	Version       string `json:"version"`
	JournalID     string `json:"journalId"`
	RequestID     string `json:"requestId"`
	State         string `json:"state"`
	Effects       string `json:"effects"`
	RecordedState string `json:"recordedState,omitempty"`
	Mode          string `json:"mode,omitempty"`
	OperationID   string `json:"operationId,omitempty"`
}

func runJournalRecovery(args []string, out, diagnostics io.Writer) error {
	fs := flag.NewFlagSet("dill agent journal-recovery", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Never echo arbitrary options or values.
	dir := fs.String("state-dir", "", "existing private deployment history directory")
	journalID := fs.String("journal-id", "", "journal ID captured with the original request")
	requestID := fs.String("request-id", "", "original deployment request ID")
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		if _, err := fmt.Fprintln(diagnostics, journalRecoveryUsage); err != nil {
			return errJournalRecoveryOutput
		}
		return nil
	} else if err != nil || fs.NArg() != 0 || *dir == "" || !validApplyID(*journalID) || !validApplyID(*requestID) {
		return errors.New(journalRecoveryUsage)
	}
	evidence, err := inspectJournalRecovery(*dir, *journalID, *requestID)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(evidence); err != nil {
		return errJournalRecoveryOutput
	}
	return nil
}

func inspectJournalRecovery(path, journalID, requestID string) (journalRecoveryEvidence, error) {
	file, _, err := readOfflineDeploymentJournal(path)
	if err != nil {
		return journalRecoveryEvidence{}, err
	}
	if file.Journal == "" {
		return journalRecoveryEvidence{}, errJournalRecoveryUnbound
	}
	if file.Journal != journalID {
		return journalRecoveryEvidence{}, errJournalRecoveryMismatch
	}
	evidence := journalRecoveryEvidence{
		Version: "dill.agent-journal-recovery/1", JournalID: file.Journal, RequestID: requestID,
		State: "missing", Effects: "unknown",
	}
	for _, r := range file.Rows {
		if r.RequestID != requestID {
			continue
		}
		evidence.RecordedState, evidence.Effects = r.State, r.Effects
		evidence.Mode, evidence.OperationID = r.Mode, r.OperationID
		evidence.State = r.State
		if r.State == "pending" {
			// Offline there is no running operation to observe and no outcome
			// to infer. Preserve pending separately as the exact durable state.
			evidence.State = "unknown"
		}
		break
	}
	return evidence, nil
}
