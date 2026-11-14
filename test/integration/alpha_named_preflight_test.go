//go:build integration && (linux || darwin)

package integration

import (
	"os"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

// Actual independently provisioned native fixture, never Brine/source proof.
func (f *alphaQualifiedFixture) namedReview(t *testing.T, action string, extra ...string) preflight.Result {
	t.Helper()
	result, code := f.namedAttempt(t, action, "60s", extra...)
	if code != 0 || result.Outcome != "succeeded" || result.Target == nil || len(result.Resources) != 3 || result.SelectionDigest == nil {
		t.Fatal("complete native named review unavailable")
	}
	expected := map[string]string{"container": f.stack + "_app", "network": f.stack, "volume": f.stack + "_data"}
	for _, row := range result.Resources {
		if expected[row.Kind] != row.Name {
			t.Fatal("review did not expose the actual bounded fixture names")
		}
		delete(expected, row.Kind)
		if row.Presence == "present" {
			switch row.Kind {
			case "container":
				if runEngine(t, "docker", "inspect", "--format", "{{.Id}}", row.Name) != row.Identity {
					t.Fatal("reviewed container identity not actual")
				}
			case "network":
				if runEngine(t, "docker", "network", "inspect", "--format", "{{.Id}}", row.Name) != row.Identity {
					t.Fatal("reviewed network identity not actual")
				}
			case "volume":
				if runEngine(t, "docker", "volume", "inspect", "--format", "{{.Name}}", row.Name) != row.Identity {
					t.Fatal("reviewed named volume identity not actual")
				}
			}
		}
	}
	if len(expected) != 0 {
		t.Fatal("review omitted a declared fixture resource")
	}
	return result
}

func TestAlphaQualifiedNamedPreflightFiveRoutes(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	original, err := os.ReadFile(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"up", "start", "restart", "stop", "down"} {
		extra := []string{}
		if action == "up" {
			extra = []string{"--no-prune", "--pull", "never"}
		}
		review := f.namedReview(t, action, extra...)
		args := append(append([]string{}, extra...), "--preflight-contract", preflight.Version, "--expect-preflight", *review.SelectionDigest)
		result, code := f.run(t, action, "cli", "", args...)
		if action == "down" {
			alphaQualifiedAxes(t, result, code, 1, "failed", "partial")
		} else {
			alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
		}
		if action == "up" {
			if image := runEngine(t, "docker", "inspect", "--format", "{{.Image}}|{{.Config.Image}}", f.stack+"_app"); image != f.imageID+"|"+f.imageID {
				t.Fatal("bound creation did not consume exact inspected immutable content/reference")
			}
			runEngine(t, "docker", "exec", f.stack+"_app", "sh", "-c", "printf synthetic-named-v2 > /data/value")
			repeat := f.namedReview(t, "up", extra...)
			for _, row := range repeat.Resources {
				if row.Kind == "container" && row.Action != "ensure-started" {
					t.Fatal("prepared ID polluted original desired-reference hash/no-op provenance")
				}
			}
		}
		f.assertForeign(t)
	}
	after, err := os.ReadFile(f.cfg)
	if err != nil || string(after) != string(original) {
		t.Fatal("handler rewrote desired source provenance")
	}
	alphaNamedData(t, f)
}

func TestAlphaQualifiedNamedPreflightMismatchBeforeEnsure(t *testing.T) {
	for _, kind := range []string{"force", "config", "container-id", "prepared-tag-content", "original-image-reference"} {
		t.Run(kind, func(t *testing.T) {
			f := newAlphaQualifiedFixture(t)
			f.seed(t)
			review := f.namedReview(t, "up", "--no-prune", "--pull", "never")
			extra := []string{}
			// Controlled fixture changes are OUTSIDE the measured checked-apply attempt.
			switch kind {
			case "force":
				extra = []string{"--force-recreate"}
			case "config":
				f.write(t, f.image, "volumes = List(\"data:/data\")\nenvironment { [\"NAMED_VARIANT\"] = \"changed\" }", false)
			case "container-id":
				old := runEngine(t, "docker", "inspect", "--format", "{{.Id}}", f.stack+"_app")
				runEngine(t, "docker", "rm", "-f", f.stack+"_app")
				f.seed(t)
				if runEngine(t, "docker", "inspect", "--format", "{{.Id}}", f.stack+"_app") == old {
					t.Fatal("fixture did not replace actual container identity")
				}
			case "prepared-tag-content":
				name := f.stack + "-image-preparer"
				t.Cleanup(func() { alphaCleanupResource(t, "docker", "container", name) })
				runEngine(t, "docker", "create", "--name", name, f.imageID)
				runEngine(t, "docker", "commit", "--change", "LABEL dill.alpha.variant=named-v2", name, f.image)
				runEngine(t, "docker", "rm", name)
				if runEngine(t, "docker", "image", "inspect", "--format", "{{.Id}}", f.image) == f.imageID {
					t.Fatal("fixture did not change prepared content")
				}
				if volumes := runEngine(t, "docker", "image", "inspect", "--format", "{{json .Config.Volumes}}", f.image); volumes != "null" && volumes != "{}" {
					t.Fatal("prepared fixture gained image VOLUME")
				}
			case "original-image-reference":
				alias := "localhost/" + f.stack + ":same-content"
				t.Cleanup(func() { alphaCleanupResource(t, "docker", "image", alias) })
				runEngine(t, "docker", "tag", f.imageID, alias)
				f.write(t, alias, `volumes = List("data:/data")`, false)
			}
			alphaNamedMismatch(t, f, *review.SelectionDigest, extra...)
		})
	}
}
