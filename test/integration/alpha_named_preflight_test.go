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
	return f.namedReviewResources(t, action, map[string]string{"container": f.stack + "_app", "network": f.stack, "volume": f.stack + "_data"}, extra...)
}

// Expected membership is prescribed by the fixture, NEVER candidate coverage.
// Successful full inventories independently establish BOTH presence and absence.
func (f *alphaQualifiedFixture) namedReviewResources(t *testing.T, action string, expected map[string]string, extra ...string) preflight.Result {
	t.Helper()
	result, code := f.namedAttempt(t, action, "60s", extra...)
	if code != 0 || result.Outcome != "succeeded" || result.Target == nil || len(result.Resources) != len(expected) || result.SelectionDigest == nil {
		t.Fatal("complete native named review unavailable")
	}
	actual := alphaNamedResourceInventory(t)
	seen := map[string]bool{}
	for _, row := range result.Resources {
		if expected[row.Kind] != row.Name || seen[row.Kind] {
			t.Fatal("review membership differs from independently prescribed fixture")
		}
		seen[row.Kind] = true
		identity, present := actual[row.Kind][row.Name]
		if !present {
			if row.Presence != "absent" || row.IdentityKind != "planned-name" || row.Identity != row.Name {
				t.Fatal("absent resource did not retain its actual planned name")
			}
		} else {
			kind := "immutable-id"
			if row.Kind == "volume" {
				kind = "engine-name"
			}
			if row.Presence != "present" || row.IdentityKind != kind || row.Identity != identity {
				t.Fatal("reviewed presence/identity differs from actual read-only inventory")
			}
		}
	}
	if len(seen) != len(expected) {
		t.Fatal("review omitted a prescribed fixture resource")
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
