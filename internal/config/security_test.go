package config

import "testing"

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
