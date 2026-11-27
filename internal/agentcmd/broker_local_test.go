package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func localBrokerTestOptions(t *testing.T) localBrokerOptions {
	base := t.TempDir()
	return localBrokerOptions{action: "journal", socket: "/run/docker.sock", daemon: "daemon-test", state: filepath.Join(base, "coord"), store: filepath.Join(base, "runtime"), history: historyDirectory(t), root: filepath.Join(base, "stages"), catalog: filepath.Join(base, "catalog"), pkl: "/operator/pkl"}
}
func TestBrokerLocalJournalNeverRecreatesMissingHistory(t *testing.T) {
	o := localBrokerTestOptions(t)
	_, err := runLocalBroker(context.Background(), o, nil)
	if err == nil {
		t.Fatal("recreated missing protected history")
	}
	if _, err = os.Stat(filepath.Join(o.history, "operations.json")); !os.IsNotExist(err) {
		t.Fatal("missing state was recreated")
	}
}
func TestBrokerLocalJournalAndFenceUseDurableSameRequests(t *testing.T) {
	o := localBrokerTestOptions(t)
	target := brokersecret.Digest([]byte("dill-broker-local/1\x00" + o.socket + "\x00" + o.daemon))
	history, err := openDeploymentHistory(o.history, target)
	if err != nil {
		t.Fatal(err)
	}
	journal := history.journal
	history.dir.Close()
	reply, err := runLocalBroker(context.Background(), o, nil)
	if err != nil || reply.Journal != journal || reply.Status != 200 {
		t.Fatal("journal discovery failed", err)
	}
	id := strings.Repeat("1", 32)
	o.action = "fence"
	o.journal = journal
	body, _ := json.Marshal(map[string]string{"requestId": id, "stack": "demo", "confirm": "fence-if-unadmitted"})
	reply, err = runLocalBroker(context.Background(), o, bytes.NewReader(body))
	if err != nil || !bytes.Contains(reply.Body, []byte("not-admitted")) {
		t.Fatal("fence did not persist", err)
	}
	o.action = "status"
	o.id = id
	reply, err = runLocalBroker(context.Background(), o, nil)
	if err != nil || !bytes.Contains(reply.Body, []byte("not-admitted")) {
		t.Fatal("lost fence after CLI reopen", err)
	}
	o.journal = strings.Repeat("f", 32)
	if _, err = runLocalBroker(context.Background(), o, nil); err == nil {
		t.Fatal("journal mismatch accepted")
	}
}
func TestBrokerLocalParserClosedNamespace(t *testing.T) {
	args := []string{"status", "--contract", brokersecret.Version, "--engine-socket", "/run/docker.sock", "--daemon-id", "daemon-test", "--coordination-state-dir", "/private/coord", "--broker-store", "/private/runtime", "--history-dir", "/private/history", "--staging-root", "/private/stages", "--staging-catalog", "/private/catalog", "--pkl", "/operator/pkl", "--journal-id", strings.Repeat("a", 32), "--request-id", strings.Repeat("b", 32)}
	if _, err := parseLocalBroker(args); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"-v"}, {"--engine-socket", "/other.sock"}, {"--secret-policy", "private"}, {"--pkl", "relative"}} {
		bad := append(append([]string{}, args...), extra...)
		if _, err := parseLocalBroker(bad); err == nil {
			t.Fatal("unknown/duplicate flags accepted")
		}
	}
}
