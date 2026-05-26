package orchestrator

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIsPortConflict(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"address already in use", true},
		{"bind: address already in use", true},
		{"port is already allocated", true},
		{"some other error", false},
		{"connection refused", false},
		{"no such container", false},
	}
	for _, tc := range tests {
		got := isPortConflict(errors.New(tc.msg))
		if got != tc.want {
			t.Errorf("isPortConflict(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func TestContainerStatusJSON(t *testing.T) {
	cs := ContainerStatus{
		Name:   "myapp_web",
		ID:     "abc123def456",
		State:  "running",
		Status: "Up 2 hours",
		Image:  "nginx:alpine",
		Ports:  []string{"0.0.0.0:8080->80/tcp"},
	}

	b, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"name", "id", "state", "status", "image", "ports"} {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON output missing key %q", key)
		}
	}
	if m["name"] != "myapp_web" {
		t.Errorf("name = %v, want myapp_web", m["name"])
	}
}
