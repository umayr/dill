//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeclaredVolumeOwnershipRealEngine(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dillvol%d", time.Now().UnixNano())
	ext := stack + "outside"
	managed := stack + "_cache"
	file := filepath.Join(t.TempDir(), "compose.pkl")
	body := fmt.Sprintf(`amends %q
config {
 name = %q; engine = %q
 volumes { ["database"] { ownership = "external"; name = %q }; ["cache"] { ownership = "managed"; labels { ["testcase"] = "declared" } } }
 services { ["app"] { image = "docker.io/library/busybox:1.36"; command = List("sleep", "300"); volumes = List("database:/db", "cache:/cache") } }
}`, filepath.Join(root, "dill.pkl"), stack, env.engine, ext)
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, vol := range []string{managed, ext} {
			if volumeExists(t, env.engine, vol) {
				runEngine(t, env.engine, "volume", "rm", vol)
			}
		}
	})
	cleanupStack(t, bin, file, stack)
	run := func(args ...string) (string, error) {
		out, err := exec.Command(bin, append([]string{"-f", file}, args...)...).CombinedOutput()
		return string(out), err
	}
	if out, err := run("up"); err == nil || !strings.Contains(out, "is missing") {
		t.Fatalf("missing external not refused: %v %s", err, out)
	}
	if volumeExists(t, env.engine, ext) || volumeExists(t, env.engine, managed) {
		t.Fatal("preflight created a volume")
	}
	runEngine(t, env.engine, "volume", "create", ext)
	out, _ := run("plan")
	if !strings.Contains(out, "reuse external volume "+ext) || !strings.Contains(out, "create managed volume "+managed) {
		t.Fatal(out)
	}
	runEngine(t, env.engine, "volume", "create", managed)
	if out, err := run("up"); err == nil || !strings.Contains(out, "retained") {
		t.Fatalf("did not refuse foreign volume: %s %v", out, err)
	}
	runEngine(t, env.engine, "volume", "rm", managed)
	if out, err := run("up"); err != nil {
		t.Fatalf("create: %s %v", out, err)
	}
	if got := runEngine(t, env.engine, "volume", "inspect", "--format", `{{index .Labels "testcase"}}`, managed); got != "declared" {
		t.Fatal("managed labels missing")
	}
}
