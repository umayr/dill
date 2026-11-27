package agentcmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/projectsnapshot"
)

const (
	correlationTestJournal = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// Canonical manifest for the existing `main.pkl` = `abc` upload example
	// in docs/snapshot-staging.md. Hash these literal catalog bytes, not a
	// decoded/re-encoded manifest or the example's upload/file bytes.
	correlationTestManifest = `{"schema":"dill.snapshot.manifest.v1","entrypoint":"main.pkl","files":[{"path":"main.pkl","length":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}]}`
	correlationTestDigest   = "427ef7d0b2a72bb31bb64a9ae74a82850f3ee1e892e98ac26ff05ba909d21f5c"
)

func correlationTestRow(n int, stack, mode, state, effects string) deploymentRecord {
	r := deploymentRecord{
		deploymentStatus: deploymentStatus{RequestID: fmt.Sprintf("%032x", n), Stack: stack, State: state, Effects: effects},
		Mode:             mode, StageID: strings.Repeat("b", 32), Manifest: correlationTestDigest,
	}
	if state == "succeeded" || effects == "partial" {
		r.OperationID = fmt.Sprintf("op_%032x", 100+n)
	}
	if state == "succeeded" && (mode == "create" || mode == "update") {
		r.Owned = []operation.ManagedResource{
			{Kind: "container", Name: "synthetic-container", ID: strings.Repeat("c", 64)},
			{Kind: "network", Name: "synthetic-network", ID: strings.Repeat("d", 64)},
		}
	}
	return r
}

func TestDeploymentCorrelationExactEncoding(t *testing.T) {
	file := []byte("abc")
	snapshot := projectsnapshot.Snapshot{Entrypoint: "main.pkl", Files: []projectsnapshot.Entry{{Path: "main.pkl", Content: file, SHA256: sha256.Sum256(file)}}}
	catalogBytes, err := projectsnapshot.CanonicalManifest(t.Context(), snapshot, projectsnapshot.MaximumLimits())
	if err != nil || string(catalogBytes) != correlationTestManifest {
		t.Fatalf("catalog fixture bytes = %q, %v", catalogBytes, err)
	}
	if digest := fmt.Sprintf("%x", sha256.Sum256([]byte(correlationTestManifest))); digest != correlationTestDigest {
		t.Fatalf("catalog fixture bytes hash to %s, want %s", digest, correlationTestDigest)
	}
	create := correlationTestRow(1, "stack", "create", "succeeded", "complete")
	create.Manifest = strings.Repeat("f", 64) // A different reviewed input on the same stack.
	update := correlationTestRow(2, create.Stack, "update", "succeeded", "complete")
	original := deploymentHistoryFile{Version: 2, Target: operation.Digest("test target"), Journal: correlationTestJournal, Rows: []deploymentRecord{create, update}}
	before, err := canonicalHistory(original)
	if err != nil {
		t.Fatal(err)
	}
	for i, wantDigest := range []string{strings.Repeat("f", 64), correlationTestDigest} {
		row := original.Rows[i]
		got, err := projectDeploymentCorrelation(original, correlationTestJournal, row.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"version":"dill.agent-deployment-correlation/1","journalId":%q,"requestId":%q,"mode":%q,"recordedState":"succeeded","state":"succeeded","effects":"complete","operationId":%q,"manifestEvidence":"recorded","manifestSha256":%q}`,
			correlationTestJournal, row.RequestID, row.Mode, row.OperationID, wantDigest)
		if string(encoded) != want {
			t.Fatalf("correlation = %s, want %s", encoded, want)
		}
	}
	after, err := canonicalHistory(original)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("projection changed its input")
	}
}

func TestDeploymentCorrelationUncertainty(t *testing.T) {
	create := correlationTestRow(1, "stack", "create", "succeeded", "complete")
	compacted := create
	compacted.StageID, compacted.Manifest, compacted.Owned = "", "", nil
	update := correlationTestRow(2, create.Stack, "update", "succeeded", "complete")
	pending := correlationTestRow(3, "pending", "create", "pending", "unknown")
	unknown := correlationTestRow(4, "unknown", "create", "unknown", "unknown")
	partial := correlationTestRow(5, "partial", "create", "failed", "partial")
	fence := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: fmt.Sprintf("%032x", 6), Stack: create.Stack, State: "not-admitted", Effects: "none"}, Mode: "fence"}
	failedNoEffects := correlationTestRow(7, "failure", "create", "failed", "none")
	failedNoEffects.OperationID = fmt.Sprintf("op_%032x", 107)
	compactedFailure := failedNoEffects
	compactedFailure.StageID, compactedFailure.Manifest = "", ""
	remove := correlationTestRow(8, create.Stack, "remove", "succeeded", "complete")
	missing := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: strings.TrimPrefix(create.OperationID, "op_")}}

	for _, tc := range []struct {
		name, state, effects, manifestEvidence, digest string
		version                                        int
		rows                                           []deploymentRecord
		selected                                       deploymentRecord
	}{
		{"missing, even if a Dill ID has this suffix", "missing", "unknown", "missing", "", 2, []deploymentRecord{create}, missing},
		{"compacted create with a newer full update", "succeeded", "complete", "compacted", "", 2, []deploymentRecord{compacted, update}, compacted},
		{"compacted no-effects failure retains operation ID", "failed", "none", "compacted", "", 2, []deploymentRecord{compactedFailure}, compactedFailure},
		{"legacy with journal identity and a stored digest", "succeeded", "complete", "legacy", "", 1, []deploymentRecord{create}, create},
		{"pending is not an outcome", "unknown", "unknown", "pending", "", 2, []deploymentRecord{pending}, pending},
		{"recorded unknown remains unknown", "unknown", "unknown", "recorded", correlationTestDigest, 2, []deploymentRecord{unknown}, unknown},
		{"partial effects are not success", "failed", "partial", "recorded", correlationTestDigest, 2, []deploymentRecord{partial}, partial},
		{"full no-effects failure remains failed", "failed", "none", "recorded", correlationTestDigest, 2, []deploymentRecord{failedNoEffects}, failedNoEffects},
		{"fence proves not admitted", "not-admitted", "none", "not-applicable", "", 2, []deploymentRecord{create, fence}, fence},
		{"remove does not project a manifest", "succeeded", "complete", "not-applicable", "", 2, []deploymentRecord{compacted, remove}, remove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := deploymentHistoryFile{Version: tc.version, Target: operation.Digest("test target"), Journal: correlationTestJournal, Rows: tc.rows}
			got, err := projectDeploymentCorrelation(original, correlationTestJournal, tc.selected.RequestID)
			want := deploymentCorrelation{
				Version: "dill.agent-deployment-correlation/1", JournalID: correlationTestJournal, RequestID: tc.selected.RequestID,
				Mode: tc.selected.Mode, RecordedState: tc.selected.State, State: tc.state, Effects: tc.effects, OperationID: tc.selected.OperationID,
				ManifestEvidence: tc.manifestEvidence, ManifestSHA256: tc.digest,
			}
			if err != nil || got != want {
				t.Fatalf("correlation = %+v, %v; want %+v", got, err, want)
			}
			encoded, err := json.Marshal(got)
			if err != nil || bytes.Contains(encoded, []byte(`"manifestSha256"`)) != (tc.digest != "") ||
				bytes.Contains(encoded, []byte(`"operationId"`)) != (tc.selected.OperationID != "") ||
				bytes.Contains(encoded, []byte(`"recordedState"`)) != (tc.selected.State != "") {
				t.Fatalf("incorrect optional fields: %s, %v", encoded, err)
			}
		})
	}
}

func TestDeploymentCorrelationRejectsInvalidSelectedRow(t *testing.T) {
	row := correlationTestRow(1, "stack", "create", "succeeded", "complete")
	row.Manifest = "sha256:" + row.Manifest // Journal values are unprefixed; do not normalize.
	original := deploymentHistoryFile{Version: 2, Target: operation.Digest("test target"), Journal: correlationTestJournal, Rows: []deploymentRecord{row}}
	got, err := projectDeploymentCorrelation(original, correlationTestJournal, row.RequestID)
	if err != errJournalStatusInvalid || got != (deploymentCorrelation{}) {
		t.Fatalf("invalid selected record returned %+v, %v", got, err)
	}
}

func TestDeploymentCorrelationBinding(t *testing.T) {
	row := correlationTestRow(1, "stack", "create", "succeeded", "complete")
	for _, tc := range []struct {
		name, journal, expectedJournal, requestID string
		version                                   int
	}{
		{"unbound legacy", "", correlationTestJournal, row.RequestID, 1},
		{"different history", correlationTestJournal, strings.Repeat("d", 32), row.RequestID, 2},
		{"missing expected identity", correlationTestJournal, "", row.RequestID, 2},
		{"Dill operation ID used as request ID", correlationTestJournal, correlationTestJournal, row.OperationID, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := deploymentHistoryFile{Version: tc.version, Target: operation.Digest("test target"), Journal: tc.journal, Rows: []deploymentRecord{row}}
			got, err := projectDeploymentCorrelation(original, tc.expectedJournal, tc.requestID)
			if err != errDeploymentCorrelationBinding || got != (deploymentCorrelation{}) {
				t.Fatalf("invalid binding returned %+v, %v", got, err)
			}
		})
	}
}
