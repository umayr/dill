package config

import (
	"strings"
	"testing"
)

func TestValidateRuntimeDuplicateContainerName(t *testing.T) {
	cfg := &DillConfig{Services: map[string]*Service{"a": {ContainerName: "same"}, "b": {ContainerName: "same"}}}
	if err := ValidateRuntime(cfg); err == nil || !strings.Contains(err.Error(), `service "b" containerName "same" duplicates service "a"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateGraph(t *testing.T) {
	tests := []struct {
		name     string
		services map[string]*Service
		want     string
	}{
		{"valid", map[string]*Service{"a": {DependsOn: []string{"b"}}, "b": {}}, ""},
		{"missing", map[string]*Service{"a": {DependsOn: []string{"missing"}}}, `service "a" dependsOn[0]: "missing" is not defined`},
		{"cycle", map[string]*Service{"a": {DependsOn: []string{"b"}}, "b": {DependsOn: []string{"a"}}}, `cycle detected involving service "a"`},
		{"nil", map[string]*Service{"a": nil}, `service "a" is nil`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGraph(tt.services)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %s", err, tt.want)
			}
		})
	}
}
