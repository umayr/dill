//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Exercise the public file format through Pkl and the CLI. Unlike the
// moving-tag registry pilot, this contract test needs no daemon or network.
func TestImageLockCLIContract(t *testing.T) {
	root := repoRoot(t)
	bin := dillBinary(t, root)
	dir := t.TempDir()
	project := filepath.Join(dir, "compose.pkl")
	lock := filepath.Join(dir, "dill.lock")
	digest := "sha256:" + strings.Repeat("a", 64)
	jobDigest := "sha256:" + strings.Repeat("b", 64)
	web := "alpine@" + digest
	job := "busybox@" + jobDigest
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	projectWith := func(image string) {
		write(project, fmt.Sprintf(`amends %q
config {
 name = "lock-test"
 services { ["web"] { image = %q } }
 jobs { ["check"] { image = %q; command = List("true") } }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), image, job))
	}
	projectWith(web)
	run := func(args ...string) string { t.Helper(); return runDill(t, true, bin, args...) }
	run("lock", "--file", project)
	raw, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"version":1,"services":{"web":{"image":%q,"digest":%q}},"jobs":{"check":{"image":%q,"digest":%q}}}`, web, "docker.io/library/"+web, job, "docker.io/library/"+job)
	var a, b any
	if err = json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("unexpected lock: %s", raw)
	}
	// A committed lock may map a mutable tag to the same digest. Reading or
	// regenerating a matching lock must never need to look that tag up.
	tag := "localhost:1/web:latest"
	projectWith(tag)
	tagged := strings.ReplaceAll(strings.Replace(want, "docker.io/library/"+web, "localhost:1/web@"+digest, 1), web, tag)
	write(lock, tagged)
	run("lock", "--file", project)
	cfg := run("config", "-f", project)
	var images struct {
		Services, Jobs map[string]struct{ Image string }
	}
	if err := json.Unmarshal([]byte(cfg), &images); err != nil {
		t.Fatal(err)
	}
	if images.Services["web"].Image != "localhost:1/web@"+digest || images.Jobs["check"].Image != "docker.io/library/"+job {
		t.Fatalf("wrong effective images: %+v", images)
	}
	for _, tc := range []struct{ name, doc, diagnostic string }{
		{"unknown version", strings.Replace(tagged, `"version":1`, `"version":2`, 1), "version"},
		{"missing job", strings.Replace(tagged, fmt.Sprintf(`"check":{"image":%q,"digest":%q}`, job, "docker.io/library/"+job), "", 1), "stale"},
		{"stale service", strings.Replace(tagged, tag, "localhost:1/web:old", 1), "stale"},
		{"different repository", strings.Replace(tagged, "localhost:1/web@", "localhost:1/other@", 1), "repository"},
		{"duplicate section", strings.Replace(tagged, `"jobs":`, `"jobs":{},"jobs":`, 1), "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(lock, tc.doc)
			out, err := exec.Command(bin, "config", "-f", project).CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.diagnostic) {
				t.Fatalf("expected %q, got %v: %s", tc.diagnostic, err, out)
			}
		})
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(run("config", "-f", project)), &images); err != nil || images.Services["web"].Image != tag {
		t.Fatalf("unlocked config: %+v, %v", images, err)
	}
}
