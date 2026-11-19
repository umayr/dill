package agentcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
)

func TestDeploymentRetentionKeepsExactReplayAndOwnership(t *testing.T) {
	path, target := historyDirectory(t), operation.Digest("retention target")
	h, err := openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	journal := h.journal
	id := func(n int) string { return fmt.Sprintf("%032x", n) }
	stage, manifest := strings.Repeat("a", 32), strings.Repeat("b", 64)
	row := func(n int, stack, mode string) deploymentRecord {
		return deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id(n), Stack: stack, State: "pending", Effects: "unknown"}, Mode: mode, StageID: stage, Manifest: manifest}
	}
	run := func(n int, stack, mode, previous string) {
		t.Helper()
		r := row(n, stack, mode)
		if err := h.reserve(r, previous); err != nil {
			t.Fatal(err)
		}
		result := r.deploymentStatus
		result.State, result.Effects, result.OperationID = "succeeded", "complete", "op_"+id(n)
		var owned []operation.ManagedResource
		if mode != "remove" {
			owned = []operation.ManagedResource{{Kind: "container", Name: stack + "_web", ID: id(n) + id(n)}, {Kind: "network", Name: stack, ID: strings.Repeat("a", 64)}}
		}
		if err := h.finish(result, owned); err != nil {
			t.Fatal(err)
		}
	}
	run(1, "demo", "create", "")
	for i := 2; i <= 140; i++ {
		run(i, "demo", "update", id(i-1))
	}
	if h.rows[0].StageID != "" || len(h.rows[0].Owned) != 0 || len(h.rows[139].Owned) != 2 {
		t.Fatal("incorrect compacted payloads")
	}
	run(141, "removed", "create", "")
	run(142, "removed", "remove", id(141))
	if h.rows[141].StageID != "" {
		t.Fatal("removal tombstone kept a snapshot")
	}
	if _, err := h.fence(id(143), "demo"); err != nil {
		t.Fatal(err)
	}
	run(144, "uncertain", "create", "")
	if err := h.reserve(row(145, "uncertain", "update"), id(144)); err != nil {
		t.Fatal(err)
	}
	if err := h.dir.Close(); err != nil {
		t.Fatal(err)
	}
	h, err = openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	if h.journal != journal {
		t.Fatal("journal identity changed")
	}
	for n, state := range map[int]string{1: "succeeded", 142: "succeeded", 143: "not-admitted", 145: "unknown"} {
		if s, ok := h.status(id(n)); !ok || s.State != state {
			t.Fatalf("record %d lost: %v", n, s)
		}
	}
	if latest, mode := h.managed("demo"); mode != "update" || latest.RequestID != id(140) || latest.Owned[0].ID != id(140)+id(140) {
		t.Fatalf("ownership lost: %s %v", mode, latest)
	}
	if latest, mode := h.managed("removed"); mode != "" || latest.RequestID != id(142) {
		t.Fatal("tombstone lost")
	}
	if _, mode := h.managed("uncertain"); mode != "" || len(h.rows[143].Owned) != 2 {
		t.Fatal("unresolved update lost prior ownership or allowed writes")
	}
	for _, n := range []int{1, 143} {
		if err := h.reserve(row(n, "demo", "update"), id(140)); err == nil {
			t.Fatalf("ID %d replayed", n)
		}
	}
	if err := h.reserve(row(146, "demo", "update"), id(140)); err != nil {
		t.Fatal("fresh request refused:", err)
	}
}

func TestDeploymentRetentionAtByteLimitFailsClosed(t *testing.T) {
	h, err := openDeploymentHistory(historyDirectory(t), operation.Digest("full retention target"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	stack := strings.Repeat("a", 128)
	makeRow := func(i int) deploymentRecord {
		return deploymentRecord{deploymentStatus: deploymentStatus{RequestID: fmt.Sprintf("%032x", i), Stack: stack, State: "not-admitted", Effects: "none"}, Mode: "fence"}
	}
	encoded, err := json.Marshal(makeRow(0))
	if err != nil {
		t.Fatal(err)
	}
	// Match save's encoded byte budget without doing tens of thousands of
	// fsyncs. Leave less than one fixed-width row of admission headroom.
	used, step := h.bytes, len(encoded)+1
	count := (historyByteLimit - historyOutcomeSpace - used + 1) / step
	rows := make([]deploymentRecord, count)
	for i := range rows {
		rows[i] = makeRow(i)
	}
	if err := h.save(rows, historyOutcomeSpace); err != nil {
		t.Fatal(err)
	}
	if !h.nearCapacity() {
		t.Fatal("full history not flagged")
	}
	if _, err := h.fence(makeRow(count).RequestID, stack); !errors.Is(err, errHistoryCapacity) {
		t.Fatalf("new fence at capacity: %v", err)
	}
	if s, err := h.fence(makeRow(0).RequestID, stack); err != nil || s.State != "not-admitted" {
		t.Fatalf("existing fence at capacity: %v %v", s, err)
	}
}

func TestDeploymentRetentionUpgradesFencedLegacyJournal(t *testing.T) {
	path, target := historyDirectory(t), operation.Digest("retention target")
	journal, id := strings.Repeat("a", 32), strings.Repeat("b", 32)
	raw, err := canonicalHistory(deploymentHistoryFile{Version: 1, Target: target, Journal: journal, Rows: []deploymentRecord{{deploymentStatus: deploymentStatus{RequestID: id, Stack: "demo", State: "not-admitted", Effects: "none"}, Mode: "fence"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "operations.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := openDeploymentHistory(path, target)
	if err != nil {
		t.Fatal(err)
	}
	defer h.dir.Close()
	v2, err := os.ReadFile(filepath.Join(path, "operations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if h.journal != journal || !strings.Contains(string(v2), `"version":2`) || strings.Contains(string(v2), `"stageId"`) {
		t.Fatal("legacy fence not compacted in place")
	}
	if status, ok := h.status(id); !ok || status.State != "not-admitted" {
		t.Fatal("legacy fence lost")
	}
}
