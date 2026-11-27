package brokersecret

import (
	"bytes"
	"strings"
	"testing"
)

func TestBrokerInputsClosedNamespace(t *testing.T) {
	vars := map[string]string{"BRINE_VAR_DOMAIN": "example.test", "BRINE_VAR_REGION": "west"}
	b, err := EncodeInputs(vars)
	if err != nil {
		t.Fatal(err)
	}
	i, err := DecodeInputs(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	env, err := i.Environment()
	if err != nil || strings.Join(env, "|") != "BRINE_VAR_DOMAIN=example.test|BRINE_VAR_REGION=west" {
		t.Fatal("noncanonical inputs", env, err)
	}
	for _, k := range []string{"PATH", "HOME", "LD_PRELOAD", "SSL_CERT_FILE", "BRINE_VAR_BRINE_VAR_DOMAIN", "BRINE_VAR_DOMAIN=evil", "BRINE_VAR_lower"} {
		if _, err = EncodeInputs(map[string]string{k: "anything"}); err == nil {
			t.Fatal("accepted target environment control", k)
		}
	}
	for _, raw := range []string{`{"contract":"dill.broker-inputs/1","variables":null}`, `{"contract":"dill.broker-inputs/1","variables":{},"unknown":true}`, `{"contract":"dill.broker-inputs/1","variables":{"BRINE_VAR_DOMAIN":"x","BRINE_VAR_DOMAIN":"y"}}`, `{"contract":"dill.broker-inputs/1","variables":{"PATH":"x"}}`} {
		if _, err = DecodeInputs(strings.NewReader(raw)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err = EncodeInputs(map[string]string{"BRINE_VAR_DOMAIN": "x\x00y"}); err == nil {
		t.Fatal("NUL accepted")
	}
	if _, err = DecodeInputs(strings.NewReader(strings.Repeat(" ", MaxInputs+1))); err == nil {
		t.Fatal("unbounded document")
	}
}
