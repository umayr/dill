//go:build integration && (linux || darwin)

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func alphaNamedEmptyConfig(t *testing.T, path, root, stack string) {
	t.Helper()
	body := fmt.Sprintf("amends %q\nconfig { name = %q; engine = \"docker\"; services {} }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	evaluateAlphaFixture(t, path)
}

// The genuine Pkl fixture must be empty BEFORE any native candidate can run.
// This test evaluates trusted synthetic config only; it is NOT native proof.
func TestAlphaNamedEmptyFixtureEvaluation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.pkl")
	alphaNamedEmptyConfig(t, path, repoRoot(t), "synthetic-empty-v2")
	pkl := os.Getenv("DILL_PKL_PATH")
	if pkl == "" {
		pkl = "pkl"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, pkl, "eval", path).Output()
	if err != nil {
		t.Fatal("actual empty fixture Pkl evaluation failed")
	}
	var doc struct {
		Config struct {
			Services map[string]json.RawMessage `json:"services"`
		} `json:"config"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Config.Services == nil || len(doc.Config.Services) != 0 {
		t.Fatal("empty qualification Pkl fixture did not evaluate to an actual empty service map")
	}
}

// Frozen late-secret receipt shape, not native execution or a source proof.
// The same decoder+predicate composition is used by the actual negative case.
func TestAlphaNamedDeclaredAbsentPrivacyOracle(t *testing.T) {
	const receipt = `{"version":"dill.preflight/1","operationId":"op_00000000000000000000000000000000","operation":"start","target":null,"stack":"","outcome":"failed","engineEffects":"none","coordinationEffects":"written","inputDigest":null,"selectionDigest":null,"coverage":{"containers":{"scope":"declared-stack","count":0,"presentCount":0,"complete":false},"network":{"scope":"stack-network","count":0,"presentCount":0,"complete":false},"volumes":{"scope":"declared-attached-named","count":0,"presentCount":0,"complete":false}},"resources":[],"resourcesTruncated":false,"errors":[{"code":"SECRET_UNAVAILABLE","message":"Secret configuration or source unavailable."}]}`
	valid, err := decodeAlphaNamed([]byte(receipt), 1)
	if err != nil || !alphaNamedDeclaredAbsentRefusal(valid, 1) {
		t.Fatal("exact frozen redacted refusal rejected")
	}
	if alphaNamedDeclaredAbsentRefusal(valid, 0) || alphaNamedDeclaredAbsentRefusal(valid, 2) {
		t.Fatal("wrong exit accepted")
	}
	mutations := map[string]func(map[string]any){
		"code":         func(m map[string]any) { m["errors"].([]any)[0].(map[string]any)["code"] = "PREFLIGHT_UNAVAILABLE" },
		"message":      func(m map[string]any) { m["errors"].([]any)[0].(map[string]any)["message"] = "wrong" },
		"outcome":      func(m map[string]any) { m["outcome"] = "cancelled" },
		"effects":      func(m map[string]any) { m["engineEffects"] = "complete" },
		"coordination": func(m map[string]any) { m["coordinationEffects"] = "none" },
		"operation":    func(m map[string]any) { m["operation"] = "stop" },
		"target": func(m map[string]any) {
			m["target"] = map[string]any{"engine": "docker", "endpointDigest": "sha256:" + strings.Repeat("a", 64), "qualifiedIdentityDigest": "sha256:" + strings.Repeat("b", 64)}
		},
		"stack":             func(m map[string]any) { m["stack"] = "invented-context" },
		"rows":              func(m map[string]any) { m["resources"] = []any{map[string]any{"kind": "network"}} },
		"inputDigest":       func(m map[string]any) { m["inputDigest"] = "sha256:" + strings.Repeat("a", 64) },
		"selectionDigest":   func(m map[string]any) { m["selectionDigest"] = "sha256:" + strings.Repeat("a", 64) },
		"truncated":         func(m map[string]any) { m["resourcesTruncated"] = true },
		"truncated-null":    func(m map[string]any) { m["resourcesTruncated"] = nil },
		"truncated-coerced": func(m map[string]any) { m["resourcesTruncated"] = "false" },
		"scope":             func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["scope"] = "wrong" },
		"complete":          func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["complete"] = true },
		"complete-null":     func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["complete"] = nil },
		"complete-coerced":  func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["complete"] = 0 },
		"count-null":        func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["count"] = nil },
		"count-bool":        func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["count"] = false },
		"count-fraction":    func(m map[string]any) { m["coverage"].(map[string]any)["network"].(map[string]any)["count"] = 0.5 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal([]byte(receipt), &document); err != nil {
				t.Fatal(err)
			}
			mutate(document)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			result, err := decodeAlphaNamed(data, 1)
			if err == nil && alphaNamedDeclaredAbsentRefusal(result, 1) {
				t.Fatal("incorrect privacy/phase or null/coerced evidence accepted")
			}
		})
	}
}

// Successful full listing, never failed inspect, establishes absence. Names and
// cardinality come from the prescribed fixture; no candidate coverage is echoed.
func alphaNamedResourceInventory(t *testing.T) map[string]map[string]string {
	t.Helper()
	result := map[string]map[string]string{}
	for kind, args := range map[string][]string{
		"container": {"ps", "-a", "--no-trunc", "--format", "{{.ID}}|{{.Names}}"},
		"network":   {"network", "ls", "--no-trunc", "--format", "{{.ID}}|{{.Name}}"},
		"volume":    {"volume", "ls", "--format", "{{.Name}}"},
	} {
		result[kind] = map[string]string{}
		text := runEngine(t, "docker", args...)
		if text == "" {
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Split(line, "|")
			identity, name := "", ""
			if kind == "volume" {
				if len(fields) != 1 {
					t.Fatal("malformed volume inventory")
				}
				identity, name = fields[0], fields[0]
			} else {
				if len(fields) != 2 {
					t.Fatal("malformed immutable resource inventory")
				}
				identity, name = fields[0], fields[1]
			}
			if identity == "" || name == "" || result[kind][name] != "" {
				t.Fatal("missing/duplicate actual inventory identity")
			}
			result[kind][name] = identity
		}
	}
	return result
}
