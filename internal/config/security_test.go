package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRuntimeRejectsReservedLabels(t *testing.T) {
	cfg := &DillConfig{
		Services: map[string]*Service{
			"web": {
				Image:  "nginx:alpine",
				Labels: map[string]string{"dill.stack": "other"},
			},
		},
	}
	if err := ValidateRuntime(cfg); err == nil {
		t.Fatal("expected reserved label validation error")
	}
}

func TestValidateRuntimeAcceptsUserLabels(t *testing.T) {
	cfg := &DillConfig{
		Services: map[string]*Service{
			"web": {
				Image:  "nginx:alpine",
				Labels: map[string]string{"com.example.role": "frontend"},
			},
		},
	}
	if err := ValidateRuntime(cfg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateRuntimeHealthCheckDurations(t *testing.T) {
	for _, field := range []string{"interval", "timeout", "startPeriod"} {
		for _, raw := range []string{`{"value":1,"unit":"bad"}`, `"invalid"`, `{"value":1,"unit":"s"}`} {
			t.Run(field+"/"+raw, func(t *testing.T) {
				d := json.RawMessage(raw)
				hc := &HealthCheck{Test: json.RawMessage(`"ok"`)}
				switch field {
				case "interval":
					hc.Interval = &d
				case "timeout":
					hc.Timeout = &d
				case "startPeriod":
					hc.StartPeriod = &d
				}
				err := ValidateRuntime(&DillConfig{Services: map[string]*Service{"app": {HealthCheck: hc}}})
				if (err != nil) != (raw != `{"value":1,"unit":"s"}`) {
					t.Fatalf("error = %v", err)
				}
				if err != nil && !strings.Contains(err.Error(), "app healthcheck "+field) && !strings.Contains(err.Error(), `"app" healthcheck `+field) {
					t.Fatalf("missing context: %v", err)
				}
			})
		}
	}
}
