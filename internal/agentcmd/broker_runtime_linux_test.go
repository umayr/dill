//go:build linux

package agentcmd

import (
	"bytes"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"os"
	"path/filepath"
	"testing"
)

func TestBrokerRuntimeReplyDecode(t *testing.T) {
	f := os.Getenv("DILL_BROKER_RUNTIME_FIXTURE")
	if f == "" {
		t.Skip("explicit retained fixture required")
	}
	raw, err := os.ReadFile(filepath.Join(f, "native-review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reply brokerCLIReply
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&reply); err != nil {
		t.Fatal("strict typed decode", err)
	}
	if err = brokersecret.DecodeMetadata(bytes.NewReader(raw), &reply); err != nil {
		t.Fatal("strict metadata codec", err)
	}
	if reply.Preview == nil || reply.Plan == nil || reply.Reference == nil {
		t.Fatal("missing result")
	}
	if err = reply.Preview.ValidateBroker(0); err != nil {
		t.Fatal("broker preflight validation", err)
	}
	if !(ownership("create", deploymentRecord{})).Matches(reply.Preview.Resources) {
		t.Fatal("managed create mismatch")
	}
}
