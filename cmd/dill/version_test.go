package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionFormats(t *testing.T) {
	out, _, err := captureCommand(t, "", func() error { return run([]string{"version"}) })
	if err != nil || out != version+"\n" {
		t.Fatalf("text output %q, err %v", out, err)
	}
	out, _, err = captureCommand(t, "", func() error { return run([]string{"version", "--format", "json"}) })
	var result struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal([]byte(out), &result) != nil || result.Version != version {
		t.Fatalf("JSON output %q, result %+v, err %v", out, result, err)
	}
}

func TestVersionJSONUsageFailure(t *testing.T) {
	out, _, err := captureCommand(t, "", func() error { return run([]string{"version", "--format", "json", "unexpected"}) })
	var result map[string]string
	if err == nil || exitCodeFor(err) != 2 || json.Unmarshal([]byte(out), &result) != nil || result["kind"] != "invalid_input" || !strings.Contains(result["error"], "unexpected") {
		t.Fatalf("JSON output %q, result %+v, err %v", out, result, err)
	}
}
