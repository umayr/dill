//go:build integration && linux

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This test is opt-in ONLY for disposable, explicitly qualified Linux engines.
// The ordinary distribution-Podman CI job is not sufficient qualification.
// A dedicated job must provision the documented server/launch/socket and a
// protected synthetic fixture root. Never opt in on an unverified local engine.
func TestDillQualifiedHostFileSecrets(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_SECRETS") != "1" {
		t.Skip("requires disposable qualified Alpha engine opt-in")
	}
	engine, socket, base := os.Getenv("DILL_ALPHA_ENGINE"), os.Getenv("DILL_ALPHA_SOCKET"), os.Getenv("DILL_ALPHA_FIXTURE_ROOT")
	if (engine != "docker" && engine != "podman") || !filepath.IsAbs(socket) || !filepath.IsAbs(base) {
		t.Fatal("explicit qualified engine/socket/protected fixture root required")
	}
	numeric := func(key string) int64 {
		t.Helper()
		n, err := strconv.ParseInt(os.Getenv(key), 10, 32)
		if err != nil || n < 1 {
			t.Fatalf("explicit non-root synthetic ID required: %s", key)
		}
		return n
	}
	uid, gid, hostUID, hostGID := numeric("DILL_ALPHA_CONTAINER_UID"), numeric("DILL_ALPHA_CONTAINER_GID"), numeric("DILL_ALPHA_HOST_UID"), numeric("DILL_ALPHA_HOST_GID")
	dir, err := os.MkdirTemp(base, "alpha-")
	if err != nil {
		t.Fatal("cannot provision protected synthetic directory")
	}
	defer os.RemoveAll(dir)
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("alpha-%d", time.Now().UnixNano())
	configPath, policyPath := filepath.Join(dir, "compose.pkl"), filepath.Join(dir, "policy.json")
	source1, source2, sourceMissing := filepath.Join(dir, "source-r1"), filepath.Join(dir, "source-r2"), filepath.Join(dir, "source-r3")
	values := []string{"dill-alpha-synthetic-v1", "dill-alpha-synthetic-v2"}
	for i, p := range []string{source1, source2} {
		if os.WriteFile(p, []byte(values[i]), 0400) != nil {
			t.Fatal("cannot provision synthetic source")
		}
		if err := os.Chown(p, int(hostUID), int(hostGID)); err != nil {
			// Hosted rootless jobs may provision subordinate-ID ownership using their
			// disposable runner's sudo, never by changing container namespaces.
			if out, err := exec.Command("sudo", "-n", "chown", fmt.Sprintf("%d:%d", hostUID, hostGID), p).CombinedOutput(); err != nil {
				_ = out
				t.Fatal("cannot provision synthetic mapped ownership")
			}
		}
	}
	sources := []any{}
	for i, p := range []string{source1, source2, sourceMissing} {
		sources = append(sources, map[string]any{"id": "db-source", "revision": fmt.Sprintf("r%d", i+1), "kind": "hostFile", "path": p, "uid": hostUID, "gid": hostGID, "mode": 256})
	}
	writeMetadata := func(p string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil || os.WriteFile(p, b, 0600) != nil {
			t.Fatal("cannot write synthetic private metadata")
		}
	}
	writeMetadata(policyPath, map[string]any{"version": "dill.secret-policy/1", "targets": []any{map[string]any{"id": "lab", "engine": engine, "socket": socket, "stacks": []any{map[string]any{"name": stack, "sources": sources}}}}})
	bindings := []string{}
	for i := 1; i <= 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("binding-r%d.json", i))
		writeMetadata(p, map[string]any{"version": "dill.secret-bindings/1", "target": "lab", "stack": stack, "bindings": []any{map[string]any{"name": "db", "source": "db-source", "revision": fmt.Sprintf("r%d", i)}}})
		bindings = append(bindings, p)
	}
	writeRecipe := func(grants bool) {
		t.Helper()
		declaration, grant := "", ""
		if grants {
			declaration = `secrets { new { name = "db" } }`
			grant = fmt.Sprintf(`secrets { new { name = "db"; target = "/run/secrets/db"; uid = %d; gid = %d; mode = 256 } }`, uid, gid)
		}
		text := fmt.Sprintf("amends %q\nconfig { name = %q; engine = %q; %s services { [\"app\"] { image = \"alpine:3.20\"; user = %q; command = List(\"sleep\",\"600\"); %s } } }\n", filepath.Join(root, "dill.pkl"), stack, engine, declaration, fmt.Sprintf("%d:%d", uid, gid), grant)
		if os.WriteFile(configPath, []byte(text), 0600) != nil {
			t.Fatal("cannot write synthetic recipe")
		}
	}
	writeRecipe(true)
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "DILL_SECRET_POLICY", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "CONTAINER_HOST", "CONTAINER_CONNECTION", "PODMAN_SOCKET":
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "DILL_SECRET_POLICY="+policyPath)
	call := func(binary string, args ...string) ([]byte, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = environment
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal("qualified subprocess failed to execute")
			}
			code = exit.ExitCode()
		}
		return out, code
	}
	prefix := []string{"-f", configPath, "-p", stack, "--engine", engine, "--engine-socket", socket, "--secret-target", "lab"}
	dill := func(binding string, args ...string) ([]byte, int) {
		a := append([]string{}, prefix...)
		if binding != "" {
			a = append(a, "--secret-bindings", binding)
		}
		return call(bin, append(a, args...)...)
	}
	endpointFlag := "--host"
	if engine == "podman" {
		endpointFlag = "--url"
	}
	engineCall := func(args ...string) ([]byte, int) {
		return call(engine, append([]string{endpointFlag, "unix://" + socket}, args...)...)
	}
	container := stack + "_app"
	t.Cleanup(func() {
		call(bin, "-f", filepath.Join(dir, "nonexistent.pkl"), "-p", stack, "--engine", engine, "--engine-socket", socket, "down")
	})
	assertPublic := func(out []byte) {
		t.Helper()
		for _, value := range values {
			hash := sha256.Sum256([]byte(value))
			if strings.Contains(string(out), value) || strings.Contains(string(out), hex.EncodeToString(hash[:])) {
				t.Fatal("synthetic value/value hash leaked into public output")
			}
		}
	}
	assertPlan := func(binding string, codeWant, recreate, noop int) {
		t.Helper()
		out, code := dill(binding, "plan", "--format", "json")
		assertPublic(out)
		if code != codeWant {
			t.Fatal("unexpected qualified plan exit")
		}
		var p planOutput
		if json.Unmarshal(out, &p) != nil || p.Summary.Recreate != recreate || p.Summary.Noop != noop {
			t.Fatal("unexpected qualified plan result")
		}
		for _, private := range []string{source1, source2, sourceMissing} {
			if strings.Contains(string(out), private) {
				t.Fatal("private source inventory rendered in plan")
			}
		}
	}
	out, code := dill(bindings[0], "secrets", "check", "--format", "json")
	assertPublic(out)
	if code != 0 || strings.TrimSpace(string(out)) != `{"contract":"dill.alpha-secrets/1","status":"available"}` {
		t.Fatal("qualified source availability failed")
	}
	out, code = dill(bindings[0], "up")
	assertPublic(out)
	if code != 0 {
		t.Fatal("qualified create failed")
	}
	assertPlan(bindings[0], 0, 0, 1)
	readAs := func(id int64, expected string) {
		t.Helper()
		out, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", id, gid), container, "cat", "/run/secrets/db")
		if code != 0 || string(out) != expected {
			t.Fatal("authorized mapped non-root workload could not read synthetic source")
		}
	}
	readAs(uid, values[0])
	if _, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", uid+1, gid+1), container, "cat", "/run/secrets/db"); code == 0 {
		t.Fatal("unauthorized non-root identity read synthetic source")
	}
	if _, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", uid, gid), container, "sh", "-c", "chmod u+w /run/secrets/db; printf mutation > /run/secrets/db"); code == 0 {
		t.Fatal("owner chmod/write bypassed read-only bind")
	}
	inspect := func() string {
		t.Helper()
		out, code := engineCall("inspect", container)
		if code != 0 {
			t.Fatal("qualified inspect failed")
		}
		assertPublic(out)
		var rows []struct {
			ID     string                             `json:"Id"`
			Config struct{ Labels map[string]string } `json:"Config"`
			Mounts []struct {
				Type, Source, Destination string
				RW                        bool
			}
		}
		if json.Unmarshal(out, &rows) != nil || len(rows) != 1 || rows[0].ID == "" || rows[0].Config.Labels["dill.secret-enabled"] != "true" {
			t.Fatal("inspect lacks ownership/secret marker")
		}
		found := false
		for _, m := range rows[0].Mounts {
			if m.Destination == "/run/secrets/db" {
				found = m.Type == "bind" && !m.RW && (m.Source == source1 || m.Source == source2)
			}
		}
		if !found {
			t.Fatal("inspect did not expose exact read-only host-file mount")
		}
		return rows[0].ID
	}
	before := inspect()
	for _, command := range []string{"start", "restart"} {
		if out, code := dill("", command); code == 0 {
			_ = out
			t.Fatal("direct marked lifecycle allowed")
		}
	}
	writeRecipe(false)
	for _, command := range []string{"start", "restart"} {
		if _, code := dill("", command); code == 0 {
			t.Fatal("recipe removal bypassed persisted marker")
		}
	}
	writeRecipe(true)
	if inspect() != before {
		t.Fatal("denied direct lifecycle changed container identity")
	}
	assertPlan(bindings[1], 1, 1, 0)
	out, code = dill(bindings[1], "up")
	assertPublic(out)
	if code != 0 {
		t.Fatal("explicit reference rotation failed")
	}
	readAs(uid, values[1])
	assertPlan(bindings[1], 0, 0, 1)
	before = inspect()
	out, code = dill(bindings[2], "up", "--pull", "always")
	assertPublic(out)
	if code != 1 || !strings.Contains(string(out), "Secret configuration or source unavailable.") {
		t.Fatal("missing selected source did not safely block apply")
	}
	if inspect() != before {
		t.Fatal("unavailable source caused recreation")
	}
	// Cleanup intentionally uses label-only fallback with a nonexistent recipe,
	// without bindings and after trusted policy is unavailable. Source retention
	// is checked before the harness removes its own synthetic fixture directory.
	if os.Remove(policyPath) != nil {
		t.Fatal("cannot remove synthetic policy")
	}
	call(bin, "-f", filepath.Join(dir, "nonexistent.pkl"), "-p", stack, "--engine", engine, "--engine-socket", socket, "down")
	if _, code := engineCall("inspect", container); code == 0 {
		t.Fatal("ownership-checked cleanup retained container")
	}
	for _, p := range []string{source1, source2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("Dill cleanup deleted an externally provisioned source")
		}
	}
}
