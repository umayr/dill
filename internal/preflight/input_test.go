package preflight

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sampleInput(t *testing.T) Input {
	t.Helper()
	data, err := os.ReadFile("testdata/canonical-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Input struct {
			Payload Input `json:"payload"`
		} `json:"input"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v.Input.Payload
}
func TestEffectiveInputNormalizationAndBinding(t *testing.T) {
	makeInput := func() Input {
		input := sampleInput(t)
		db := input.Services[0]
		db.Name = "db"
		db.ContainerName = "synthetic_db"
		input.Services = append(input.Services, db)
		input.Services[0].DependsOn = []string{"db"}
		return input
	}
	base := makeInput()
	original, err := base.Digest()
	if err != nil {
		t.Fatal(err)
	}
	equivalent := makeInput()
	equivalent.Services[0].DependsOn = []string{"db", "db"}
	equivalent.Services[0], equivalent.Services[1] = equivalent.Services[1], equivalent.Services[0]
	got, err := equivalent.Digest()
	if err != nil || got != original {
		t.Fatalf("order/set normalization changed: %s %v", got, err)
	}
	for name, mutate := range map[string]func(*Input){
		"dependency-only":               func(v *Input) { v.Services[0].DependsOn = []string{} },
		"prepared absent-service image": func(v *Input) { v.Services[0].PreparedImageID = "sha256:" + strings.Repeat("9", 64) },
		"original provenance":           func(v *Input) { v.Services[0].DesiredConfigHash = strings.Repeat("9", 64) },
		"timeout duration":              func(v *Input) { v.Flags.TimeoutNanos++ },
		"caller source claim":           func(v *Input) { v.Flags.CallerSource = "cli" },
		"force recreate":                func(v *Input) { v.Flags.ForceRecreate = true },
		"no prune flag":                 func(v *Input) { v.Flags.NoPrune = false },
	} {
		t.Run(name, func(t *testing.T) {
			v := makeInput()
			mutate(&v)
			got, err := v.Digest()
			if err != nil || got == original {
				t.Fatalf("changed input not bound %s %v", got, err)
			}
		})
	}
}
func TestEffectiveInputRejectsUnavailableOrUnsupported(t *testing.T) {
	for name, mutate := range map[string]func(*Input){
		"unsupported engine":       func(v *Input) { v.Engine = "podman" },
		"unknown action":           func(v *Input) { v.Operation = "teardown" },
		"zero timeout":             func(v *Input) { v.Flags.TimeoutNanos = 0 },
		"no deps":                  func(v *Input) { v.Flags.NoDeps = true },
		"pull":                     func(v *Input) { v.Flags.EffectivePullPolicy = "always" },
		"unknown actor":            func(v *Input) { v.Flags.CallerSource = "operator" },
		"short prepared image":     func(v *Input) { v.Services[0].PreparedImageID = "sha256:123" },
		"missing provenance":       func(v *Input) { v.Services[0].DesiredConfigHash = "" },
		"unknown dependency":       func(v *Input) { v.Services[0].DependsOn = []string{"missing"} },
		"duplicate service":        func(v *Input) { v.Services = append(v.Services, v.Services[0]) },
		"invalid UTF8 service key": func(v *Input) { v.Services[0].Name = "a\xff" },
		"up-only flag":             func(v *Input) { v.Operation = "stop" },
	} {
		t.Run(name, func(t *testing.T) {
			v := sampleInput(t)
			mutate(&v)
			if _, err := v.Digest(); err == nil {
				t.Fatal("accepted unsupported input")
			}
		})
	}
}
