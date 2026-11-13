package plan

import (
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestPinnedImageRepresentationRequiresContentAndDesiredProvenance(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	for _, scenario := range []string{"same", "prefix-normalized", "moved-tag", "missing-local-id", "missing-live-id", "short-id", "missing-hash", "wrong-hash", "different-live-reference", "changed-desired-reference", "changed-config"} {
		t.Run(scenario, func(t *testing.T) {
			svc := &config.Service{Image: "synthetic:original", PullPolicy: "never"}
			hash, err := config.ServiceConfigHash("stack", "app", svc)
			if err != nil {
				t.Fatal(err)
			}
			live := &orchestrator.LiveConfig{Image: id, ImageID: id, SystemLabels: map[string]string{"dill.config-hash": hash}}
			localID := id
			switch scenario {
			case "prefix-normalized":
				live.Image = strings.TrimPrefix(id, "sha256:")
			case "moved-tag":
				localID = other
			case "missing-local-id":
				localID = ""
			case "missing-live-id":
				live.ImageID = ""
			case "short-id":
				live.Image, live.ImageID, localID = "aaaaaaaaaaaa", "aaaaaaaaaaaa", "aaaaaaaaaaaa"
			case "missing-hash":
				delete(live.SystemLabels, "dill.config-hash")
			case "wrong-hash":
				live.SystemLabels["dill.config-hash"] = "wrong"
			case "different-live-reference":
				live.Image = "synthetic:different"
			case "changed-desired-reference":
				svc.Image = "synthetic:alias-same-content"
			case "changed-config":
				svc.User = "1000"
			}
			diffs, err := diffConfigs("app", svc, live, localID, "stack")
			if err != nil {
				t.Fatal(err)
			}
			noop := scenario == "same" || scenario == "prefix-normalized"
			if (len(diffs) == 0) != noop {
				t.Fatalf("diffs=%+v", diffs)
			}
			if scenario == "moved-tag" && (len(diffs) != 1 || diffs[0].Field != "image_id") {
				t.Fatalf("moved content not detected: %+v", diffs)
			}
			if scenario == "changed-desired-reference" {
				foundHash := false
				for _, diff := range diffs {
					if diff.Field == "config_hash" {
						foundHash = true
					}
				}
				if !foundHash {
					t.Fatalf("reference provenance ignored: %+v", diffs)
				}
			}
		})
	}
}
