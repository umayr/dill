//go:build integration && (linux || darwin)

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is only the Dill-side supply/usability criterion. No external Brine
// application, browser, production endpoint or real credential is involved.
func TestAlphaQualifiedCandidateHandoff(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_COORDINATION") != "1" {
		t.Skip("requires independently provisioned disposable native coordination opt-in")
	}
	bundle := os.Getenv("DILL_TEST_ALPHA_HANDOFF_DIR")
	commit, expectedBinary := os.Getenv("DILL_TEST_ALPHA_CANDIDATE_SHA"), os.Getenv("DILL_TEST_ALPHA_BINARY_SHA256")
	validHex := func(value string, size int) bool {
		raw, err := hex.DecodeString(value)
		return err == nil && len(raw) == size && strings.ToLower(value) == value
	}
	if !filepath.IsAbs(bundle) || !validHex(commit, 20) || !validHex(expectedBinary, 32) {
		t.Fatal("copied candidate supply requires explicit pinned inputs and fresh output directory")
	}
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal("candidate supply output must be new and private")
	}
	f := newAlphaQualifiedFixture(t)
	files := map[string]string{
		"bin/dill": f.bin, "bin/pkl": os.Getenv("DILL_PKL_PATH"),
		"dill.pkl":                           filepath.Join(f.root, "dill.pkl"),
		"internal/loader/pkl-toolchain.json": filepath.Join(f.root, "internal/loader/pkl-toolchain.json"),
		"docs/alpha-candidate-handoff.md":    filepath.Join(f.root, "docs/alpha-candidate-handoff.md"),
	}
	for _, directory := range []string{"test/qualification/v1", "internal/operation/testdata"} {
		err := filepath.WalkDir(filepath.Join(f.root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				t.Fatal("candidate public fixtures must be regular files, not links or private state")
			}
			relative, err := filepath.Rel(f.root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(relative)] = path
			return nil
		})
		if err != nil {
			t.Fatal("candidate public fixture enumeration failed")
		}
	}
	digests := map[string]string{}
	for relative, source := range files {
		info, err := os.Lstat(source)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("candidate supply source is not a pinned regular file")
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal("candidate supply source read failed")
		}
		digest := sha256.Sum256(data)
		digests[relative] = hex.EncodeToString(digest[:])
		destination := filepath.Join(bundle, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal("candidate supply destination directory failed")
		}
		mode := os.FileMode(0o600)
		if strings.HasPrefix(relative, "bin/") {
			mode = 0o700
		}
		if err := os.WriteFile(destination, data, mode); err != nil {
			t.Fatal("candidate supply byte copy failed")
		}
		copied, err := os.ReadFile(destination)
		if err != nil || sha256.Sum256(copied) != digest {
			t.Fatal("candidate supply checksum changed while copying")
		}
	}
	if digests["bin/dill"] != expectedBinary {
		t.Fatal("copied binary is not the pinned candidate")
	}
	// The public versioned fixture remains BYTE-IDENTICAL. Its general legacy
	// pull default is overridden explicitly, not silently rewritten for the pilot.
	f.bin = filepath.Join(bundle, "bin/dill")
	f.cfg = filepath.Join(bundle, "test/qualification/v1/valid.pkl")
	t.Setenv("DILL_PKL_PATH", filepath.Join(bundle, "bin/pkl"))
	result, code := f.run(t, "up", "brine", "", "--pull", "never")
	alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
	if image := runEngine(t, "docker", "inspect", "--format", "{{.Image}}|{{.Config.Image}}", f.stack+"_app"); image != f.imageID+"|"+f.imageID {
		t.Fatal("copied qualified caller did not create with immutable inspected content/reference")
	}
	record := alphaInspectCoordination(t, f)
	if record.Source != "brine" || record.OperationID != result.OperationID || record.State != "terminal" || record.Outcome != "succeeded" || record.Effects != "complete" || record.Target != strings.TrimPrefix(result.Target.QualifiedIdentityDigest, "sha256:") {
		t.Fatal("copied caller receipt and durable source/target contract differ")
	}
	f.assertForeign(t)
	manifest := map[string]any{
		"version": "dill.alpha-handoff/1", "status": "verified", "candidateCommit": commit,
		"binarySha256": expectedBinary, "fixtureVersion": "dill.alpha-qualification/1", "files": digests,
		"caller":                    map[string]string{"contract": "dill.operation/1", "source": "brine", "outcome": "succeeded", "effects": "complete", "qualifiedIdentityDigest": result.Target.QualifiedIdentityDigest},
		"downstreamBrineAcceptance": "not-run",
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(bundle, "handoff.json"), append(data, '\n'), 0o600) != nil {
		t.Fatal("verified Dill-side supply manifest could not be published")
	}
}
