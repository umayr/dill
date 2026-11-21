package agentcmd

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"golang.org/x/sys/unix"
)

func TestJournalRecoveryExactOutcomes(t *testing.T) {
	complete := journalStatusRow(1, "private-stack", "create", "succeeded", "complete")
	complete.Owned = []operation.ManagedResource{
		{Kind: "container", Name: "private-container", ID: strings.Repeat("d", 64)},
		{Kind: "network", Name: "private-network", ID: strings.Repeat("e", 64)},
	}
	failed := journalStatusRow(2, complete.Stack, "update", "failed", "none")
	failed.OperationID = "op_" + strings.Repeat("9", 32) // Not necessarily derived from the request ID.
	failed.StageID, failed.Manifest = "", ""             // Compaction preserves the result.
	partial := journalStatusRow(3, complete.Stack, "remove", "failed", "partial")
	fence := journalStatusFence(4, complete.Stack)
	pending := journalStatusRow(5, "other-pending", "create", "pending", "unknown")
	unknown := journalStatusRow(6, "other-unknown", "create", "unknown", "unknown")
	dir := historyDirectory(t)
	journalID := strings.Repeat("a", 32)
	raw := journalStatusFixture(t, 2, []deploymentRecord{complete, failed, partial, fence, pending, unknown})
	path := writeJournalStatusFixture(t, dir, raw)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, requestID, state, effects, recorded, mode, operationID string
	}{
		{"complete", complete.RequestID, "succeeded", "complete", "succeeded", "create", complete.OperationID},
		{"no effects", failed.RequestID, "failed", "none", "failed", "update", failed.OperationID},
		{"partial", partial.RequestID, "failed", "partial", "failed", "remove", partial.OperationID},
		{"durable fence", fence.RequestID, "not-admitted", "none", "not-admitted", "fence", ""},
		{"pending on disk", pending.RequestID, "unknown", "unknown", "pending", "create", ""},
		{"recorded unknown", unknown.RequestID, "unknown", "unknown", "unknown", "create", ""},
		{"no matching request", strings.Repeat("f", 32), "missing", "unknown", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := map[string]string{
				"version": "dill.agent-journal-recovery/1", "journalId": journalID, "requestId": tc.requestID,
				"state": tc.state, "effects": tc.effects,
			}
			if tc.recorded != "" {
				want["recordedState"], want["mode"] = tc.recorded, tc.mode
			}
			if tc.operationID != "" {
				want["operationId"] = tc.operationID
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			for _, combined := range []bool{true, false} {
				var out, diagnostics bytes.Buffer
				code := Main(journalRecoveryArgs(dir, journalID, tc.requestID), &out, &diagnostics, combined)
				var object map[string]string
				if code != 0 || diagnostics.Len() != 0 || json.Unmarshal(out.Bytes(), &object) != nil {
					t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
				}
				got, err := json.Marshal(object)
				if err != nil || !bytes.Equal(got, wantJSON) {
					t.Fatalf("got %s, want %s (err=%v)", got, wantJSON, err)
				}
			}
		})
	}
	assertJournalRecoveryUnchanged(t, dir, path, raw, before)
}

func TestJournalRecoveryRefusesUntrustedEvidence(t *testing.T) {
	journalID := strings.Repeat("a", 32)
	fence := journalStatusFence(1, "private-stack")
	otherJournal, err := canonicalHistory(deploymentHistoryFile{
		Version: 2, Target: operation.Digest("test target"), Rows: []deploymentRecord{fence}, Journal: strings.Repeat("b", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		busy bool
		want error
	}{
		{"wrong journal with same request", otherJournal, false, errJournalRecoveryMismatch},
		{"legacy without identity", journalStatusFixture(t, 1, []deploymentRecord{fence}), false, errJournalRecoveryUnbound},
		{"missing journal", nil, false, errJournalStatusMissing},
		{"busy journal", journalStatusFixture(t, 2, []deploymentRecord{fence}), true, errJournalStatusBusy},
		{"matching row in invalid journal", journalStatusFixture(t, 2, []deploymentRecord{fence, fence}), false, errJournalStatusInvalid},
		{"corrupt journal", []byte(`{"callerToken":"private-input"}`), false, errJournalStatusInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := historyDirectory(t)
			var path string
			var before os.FileInfo
			if tc.raw != nil {
				path = writeJournalStatusFixture(t, dir, tc.raw)
				var err error
				before, err = os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.busy {
				fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer unix.Close(fd)
				if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			}
			var out, diagnostics bytes.Buffer
			code := Main(journalRecoveryArgs(dir, journalID, fence.RequestID), &out, &diagnostics, true)
			if code != 1 || out.Len() != 0 || diagnostics.String() != "dill-agent: "+tc.want.Error()+"\n" {
				t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
			}
			assertJournalRecoveryUnchanged(t, dir, path, tc.raw, before)
		})
	}
}

func TestJournalRecoveryArgumentsAndOutputFailure(t *testing.T) {
	dir := historyDirectory(t)
	writeJournalStatusFixture(t, dir, journalStatusFixture(t, 2, []deploymentRecord{}))
	journalID, requestID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	valid := journalRecoveryArgs(dir, journalID, requestID)
	for _, args := range [][]string{
		{"journal-recovery"}, valid[:len(valid)-2],
		journalRecoveryArgs(dir, "private-input", requestID),
		journalRecoveryArgs(dir, journalID, "private-input"),
		append(append([]string{}, valid...), "--token", "private-input"),
		append(append([]string{}, valid...), "private-input"),
	} {
		var out, diagnostics bytes.Buffer
		if code := Main(args, &out, &diagnostics, true); code != 1 || out.Len() != 0 || diagnostics.String() != "dill-agent: "+journalRecoveryUsage+"\n" {
			t.Fatalf("code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
		}
	}
	var out, diagnostics bytes.Buffer
	if code := Main([]string{"journal-recovery", "--help"}, &out, &diagnostics, true); code != 0 || out.Len() != 0 || diagnostics.String() != journalRecoveryUsage+"\n" {
		t.Fatalf("help: code=%d out=%q diagnostics=%q", code, out.String(), diagnostics.String())
	}
	diagnostics.Reset()
	if code := Main(valid, journalStatusBrokenWriter{}, &diagnostics, true); code != 1 || diagnostics.String() != "dill-agent: "+errJournalRecoveryOutput.Error()+"\n" {
		t.Fatalf("output failure: code=%d diagnostics=%q", code, diagnostics.String())
	}
}

func journalRecoveryArgs(dir, journalID, requestID string) []string {
	return []string{"journal-recovery", "--state-dir", dir, "--journal-id", journalID, "--request-id", requestID}
}

func assertJournalRecoveryUnchanged(t *testing.T, dir, path string, raw []byte, before os.FileInfo) {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		if len(files) != 0 {
			t.Fatal("inspection initialized a journal")
		}
		return
	}
	if len(files) != 1 || files[0].Name() != "operations.json" {
		t.Fatal("inspection changed directory contents")
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("inspection changed journal metadata: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("inspection changed journal bytes: %v", err)
	}
}
