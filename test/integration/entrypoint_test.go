//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceEntrypointOverride(t *testing.T) {
	env := integrationEnv(t)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-entry-%d", time.Now().UnixNano())
	path := filepath.Join(t.TempDir(), "compose.pkl")
	write := func(entry string) {
		t.Helper()
		body := fmt.Sprintf(`amends %q
config {
 name = %q
 engine = %q
 services { ["app"] {
 image = "docker.io/library/busybox:1.36"
 %s
 command = List("sleep", "300")
 } }
}
`, filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine, entry)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(`entrypoint = List("/bin/sh", "-c", "exec \"$0\" \"$@\"")`)
	cleanupStack(t, bin, path, stack)
	up := func() { t.Helper(); runDill(t, true, bin, "-f", path, "--timeout", testTimeout(), "up") }
	id := func() string { return runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app") }
	up()
	if got := runEngine(t, env.engine, "inspect", "--format", "{{index .Config.Entrypoint 0}}", stack+"_app"); got != "/bin/sh" {
		t.Fatalf("entrypoint not applied: %q", got)
	}
	first := id()
	up()
	if id() != first {
		t.Fatal("unchanged argv recreated container")
	}
	write("")
	up()
	if id() == first {
		t.Fatal("removing override did not recreate container")
	}
}
