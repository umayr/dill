package plan

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// These are legacy environment-key heuristics, not a secret reference contract.
func TestSensitiveEnvironmentRenderedConformance(t *testing.T) {
	const old = "SENTINEL_OLD_58d2"
	const next = "SENTINEL_NEW_93af"
	for _, tc := range []struct{ name, before, after string }{
		{"changed", old, next}, {"added", "", next}, {"cleared", old, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &config.Service{Image: "busybox", Environment: map[string]string{"DB_PASSWORD": tc.after}}
			live := &orchestrator.LiveConfig{Image: "busybox", Env: map[string]string{"DB_PASSWORD": tc.before}}
			diffs, err := diffConfigs("app", svc, live, "", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			if len(diffs) != 1 || diffs[0].Field != "env.DB_PASSWORD" {
				t.Fatalf("unexpected diffs: %+v", diffs)
			}
			p := &Plan{Changes: []Change{{Service: "app", Kind: KindRecreate, Diffs: diffs}}}
			for _, format := range []string{"text", "json"} {
				t.Run(format, func(t *testing.T) {
					var b bytes.Buffer
					if format == "text" {
						Render(p, &b)
					} else if err := RenderJSON(p, &b); err != nil {
						t.Fatal(err)
					}
					out := b.String()
					if strings.Contains(out, old) || strings.Contains(out, next) {
						t.Fatalf("sentinel leaked in %s output: %s", format, out)
					}
					if !strings.Contains(out, "[redacted]") {
						t.Fatalf("redaction marker absent: %s", out)
					}
					if format == "json" && !json.Valid(b.Bytes()) {
						t.Fatalf("invalid JSON: %s", out)
					}
				})
			}
		})
	}
}
