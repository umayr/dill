package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

func TestBrokerCLIProcessParsing(t *testing.T) {
	if os.Getenv("DILL_BROKER_TEST_MAIN") == "1" {
		for i, a := range os.Args {
			if a == "--" {
				os.Args = append([]string{"dill"}, os.Args[i+1:]...)
				main()
				return
			}
		}
		os.Exit(98)
	}
	cases := [][]string{
		{"broker-profile-info", "--contract", "dill.broker-profile/1", "--broker-profile", "/operator/profile.json", "--value", "never-print-this"},
		{"-v", "broker-profile-info", "--contract", "dill.broker-profile/1", "--broker-profile", "/operator/profile.json"},
		append([]string{"broker-preflight", "-v"}, brokerTestArgs()...),
		append([]string{"broker-retirement-prepare", "-v"}, brokerTestArgs()...),
		append(append([]string{"broker-retirement-prepare"}, brokerTestArgs()...), "--values", "never-print-this"),
		append([]string{"-v", "broker-preflight"}, brokerTestArgs()...),
		append(append([]string{"broker-prepare"}, brokerTestArgs()...), "--secret-policy", "never-print-this"),
		{"broker-init", "--contract", "dill.broker-secrets/1", "--broker-store", "relative"},
	}
	for _, args := range cases {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestBrokerCLIProcessParsing$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "DILL_BROKER_TEST_MAIN=1")
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		exit, ok := err.(*exec.ExitError)
		var reply brokerCLIReply
		if !ok || exit.ExitCode() != 2 || stderr.Len() != 0 || json.Unmarshal(out.Bytes(), &reply) != nil || reply.Code != "INVALID_INPUT" || bytes.Contains(out.Bytes(), []byte("never-print-this")) {
			t.Fatal("raw CLI parsing/output boundary failed", out.String(), stderr.String(), err)
		}
	}
}
