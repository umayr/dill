//go:build integration && linux

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	t.Cleanup(func() { os.RemoveAll(dir) })
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("alpha-%d", time.Now().UnixNano())
	configPath, policyPath := filepath.Join(dir, "compose.pkl"), filepath.Join(dir, "policy.json")
	sourceDir := filepath.Join(dir, "sources")
	if os.Mkdir(sourceDir, 0700) != nil {
		t.Fatal("cannot provision synthetic source directory")
	}
	source1, source2, sourceMissing := filepath.Join(sourceDir, "source-r1"), filepath.Join(sourceDir, "source-r2"), filepath.Join(sourceDir, "source-r3")
	values := []string{"dill-alpha-synthetic-v1", "dill-alpha-synthetic-v2"}
	chownSource := func(p string) {
		t.Helper()
		if err := os.Chown(p, int(hostUID), int(hostGID)); err != nil {
			if out, err := exec.Command("sudo", "-n", "chown", fmt.Sprintf("%d:%d", hostUID, hostGID), p).CombinedOutput(); err != nil {
				_ = out
				t.Fatal("cannot provision synthetic mapped ownership")
			}
		}
	}
	for i, p := range []string{source1, source2} {
		if os.WriteFile(p, []byte(values[i]), 0400) != nil {
			t.Fatal("cannot provision synthetic source")
		}
		chownSource(p)
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
		text := fmt.Sprintf("amends %q\nconfig { name = %q; engine = %q; %s services { [\"app\"] { image = \"docker.io/library/alpine:3.20\"; user = %q; command = List(\"sleep\",\"600\"); %s } } }\n", filepath.Join(root, "dill.pkl"), stack, engine, declaration, fmt.Sprintf("%d:%d", uid, gid), grant)
		if os.WriteFile(configPath, []byte(text), 0600) != nil {
			t.Fatal("cannot write synthetic recipe")
		}
	}
	writeRecipe(true)
	keyDir, err := os.MkdirTemp(base, "keys-")
	if err != nil {
		t.Fatal("cannot provision private key directory")
	}
	t.Cleanup(func() { os.RemoveAll(keyDir) })
	keyPath := filepath.Join(keyDir, "preview.key")
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil || os.WriteFile(keyPath, key[:], 0600) != nil {
		t.Fatal("cannot provision private preview key")
	}
	clear(key[:])
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
	proofCounter := 0
	lastProofPath := ""
	proof := func(binding string, wantCode int) string {
		t.Helper()
		proofCounter++
		path := filepath.Join(keyDir, fmt.Sprintf("proof-%d.json", proofCounter))
		lastProofPath = path
		out, code := dill(binding, "plan", "--format", "json", "--proof-key-file", keyPath, "--proof-file", path)
		assertPublic(out)
		if code != wantCode {
			t.Fatal("checked proof plan failed")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("completed plan did not publish proof")
		}
		var doc struct{ Contract, Proof string }
		if json.Unmarshal(data, &doc) != nil || doc.Contract != "dill.alpha-secrets-preview/1" || len(doc.Proof) != 43 {
			t.Fatal("invalid real-binary proof interface")
		}
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("proof permissions")
		}
		return doc.Proof
	}
	// Exact candidate binary interface, including drift exit 1 before creation.
	initial := proof(bindings[0], 1)
	if initial != proof(bindings[0], 1) {
		t.Fatal("stable create plan produced unstable proof")
	}
	out, code := dill(bindings[0], "secrets", "check", "--format", "json")
	assertPublic(out)
	if code != 0 || strings.TrimSpace(string(out)) != `{"contract":"dill.alpha-secrets/1","status":"available"}` {
		t.Fatal("qualified source availability failed")
	}
	out, code = dill(bindings[0], "up")
	assertPublic(out)
	if code != 0 {
		cliDiagnostic := out
		if len(cliDiagnostic) > 8192 {
			cliDiagnostic = cliDiagnostic[:8192]
		}
		t.Logf("synthetic CLI create exit=%d diagnostic=%q", code, cliDiagnostic)
		if probe := os.Getenv("DILL_ALPHA_DIAGNOSTIC_BINARY"); engine == "podman" && probe != "" && os.Getenv("GITHUB_ACTIONS") == "true" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			cmd := exec.CommandContext(ctx, probe, "-test.run=^TestQualifiedAlphaCreateDiagnostics$", "-test.v")
			cmd.Env = append(append([]string{}, environment...), "DILL_ALPHA_DIAG_CONFIG="+configPath, "DILL_ALPHA_DIAG_BINDINGS="+bindings[0], "DILL_ALPHA_DIAG_STACK="+stack)
			diagnostic, probeErr := cmd.CombinedOutput()
			cancel()
			assertPublic(diagnostic)
			if len(diagnostic) > 16384 {
				diagnostic = diagnostic[:16384]
			}
			t.Logf("synthetic checked adapter failure probe (%v):\n%s", probeErr, diagnostic)
		}
		t.Fatal("qualified create failed")
	}
	assertPlan(bindings[0], 0, 0, 1)
	stable := proof(bindings[0], 0)
	if stable != proof(bindings[0], 0) || stable == initial {
		t.Fatal("stable proof or live identity invalidation failed")
	}
	readAs := func(id int64, expected string) {
		t.Helper()
		out, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", id, gid), container, "cat", "/run/secrets/db")
		if code != 0 || string(out) != expected {
			t.Fatal("authorized mapped non-root workload could not read synthetic source")
		}
	}
	readAs(uid, values[0])

	if proof(bindings[0], 0) != stable {
		t.Fatal("source access time invalidated proof")
	}
	// Metadata-only identity must detect a valid replacement even though inspect
	// still reports the same path. This does NOT attest the mounted inode.
	sourceStat, err := os.Stat(source1)
	if err != nil {
		t.Fatal("synthetic source stat")
	}
	replacement := source1 + "-replacement"
	if os.WriteFile(replacement, []byte(values[0]), 0400) != nil || os.Chtimes(replacement, sourceStat.ModTime(), sourceStat.ModTime()) != nil {
		t.Fatal("cannot provision valid synthetic replacement")
	}
	chownSource(replacement)
	if os.Rename(replacement, source1) != nil {
		t.Fatal("cannot replace owned synthetic source")
	}
	previous := stable
	stable = proof(bindings[0], 0)
	if stable == previous || stable != proof(bindings[0], 0) {
		t.Fatal("valid source replacement did not invalidate stably")
	}
	assertPlan(bindings[0], 0, 0, 1)
	// Same effective imported evaluation retains the proof; a changed imported
	// recipe must invalidate it even if a public plan happens to look the same.
	imported := filepath.Join(dir, "imported.pkl")
	originalRecipe, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal("synthetic recipe unavailable")
	}
	if os.WriteFile(imported, originalRecipe, 0600) != nil || os.WriteFile(configPath, []byte(fmt.Sprintf("amends %q\n", imported)), 0600) != nil {
		t.Fatal("cannot provision imported recipe")
	}
	if proof(bindings[0], 0) != stable {
		t.Fatal("semantically identical import changed proof")
	}
	changedRecipe := bytes.Replace(originalRecipe, []byte(`command = List("sleep","600")`), []byte(`command = List("sleep","601")`), 1)
	if bytes.Equal(changedRecipe, originalRecipe) || os.WriteFile(imported, changedRecipe, 0600) != nil {
		t.Fatal("cannot change synthetic import")
	}
	if proof(bindings[0], 1) == stable {
		t.Fatal("changed imported config retained approval")
	}
	if os.WriteFile(configPath, originalRecipe, 0600) != nil {
		t.Fatal("cannot restore synthetic recipe")
	}
	if proof(bindings[0], 0) != stable {
		t.Fatal("restored effective configuration changed proof")
	}
	// Test-only read-only snapshots must reproduce the actual CLI MAC before
	// their field-path diff is treated as evidence. Never print private frames,
	// key/proof values or source bytes, and never retry/rebaseline to pass.
	previewDiagnostic := func(stage string) {
		t.Helper()
		probe := os.Getenv("DILL_ALPHA_PREVIEW_DIAGNOSTIC_BINARY")
		if engine != "podman" || probe == "" || os.Getenv("GITHUB_ACTIONS") != "true" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, probe, "-test.run=^TestQualifiedAlphaPreviewSnapshot$", "-test.v")
		cmd.Env = append(append([]string{}, environment...), "DILL_ALPHA_DIAG_CONFIG="+configPath, "DILL_ALPHA_DIAG_BINDINGS="+bindings[0], "DILL_ALPHA_DIAG_STACK="+stack, "DILL_ALPHA_DIAG_PREVIEW_DIR="+keyDir, "DILL_ALPHA_DIAG_PREVIEW_PROOF="+lastProofPath, "DILL_ALPHA_DIAG_PREVIEW_STAGE="+stage)
		out, err := cmd.CombinedOutput()
		assertPublic(out)
		if len(out) > 16384 {
			out = out[:16384]
		}
		t.Logf("synthetic preview fact diagnostic stage=%s (%v):\n%s", stage, err, out)
		if err != nil {
			t.Error("synthetic preview diagnostic did not verify CLI proof")
		}
		// Continue gathering evidence; diagnostics do not replace or bypass the original lifecycle assertions.
	}
	previewDiagnostic("before")
	// Immutable resource identity and lifecycle state are confirmation inputs.
	if _, code := engineCall("stop", container); code != 0 {
		t.Fatal("cannot inject owned lifecycle fault")
	}
	stopped := proof(bindings[0], 0)
	previewDiagnostic("stopped")
	if stopped == stable {
		t.Fatal("stopped container retained approval")
	}
	if _, code := engineCall("start", container); code != 0 {
		t.Fatal("cannot restore owned lifecycle fault")
	}
	restarted := proof(bindings[0], 0)
	previewDiagnostic("restarted")
	if restarted != stable {
		t.Fatal("restored lifecycle proof changed; inspect verified field-path diagnostics")
	}

	if _, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", uid+1, gid+1), container, "cat", "/run/secrets/db"); code == 0 {
		t.Fatal("unauthorized non-root identity read synthetic source")
	}
	if _, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", uid, gid), container, "sh", "-c", "chmod u+w /run/secrets/db"); code == 0 {
		t.Fatal("owner chmod bypassed read-only bind")
	}
	if _, code := engineCall("exec", "--user", fmt.Sprintf("%d:%d", uid, gid), container, "sh", "-c", "printf mutation > /run/secrets/db"); code == 0 {
		t.Fatal("owner write bypassed read-only bind")
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
	if proof(bindings[1], 1) == stable {
		t.Fatal("reference rotation retained approval")
	}
	out, code = dill(bindings[1], "up")
	assertPublic(out)
	if code != 0 {
		t.Fatal("explicit reference rotation failed")
	}
	readAs(uid, values[1])
	assertPlan(bindings[1], 0, 0, 1)
	before = inspect()
	snapshot := func() string {
		t.Helper()
		var facts []string
		for _, args := range [][]string{{"ps", "-a", "--format", "{{.ID}} {{.State}}"}, {"images", "--format", "{{.ID}}"}, {"network", "ls", "--format", "{{.ID}}"}, {"volume", "ls", "--format", "{{.Name}}"}} {
			out, code := engineCall(args...)
			if code != 0 {
				t.Fatal("cannot observe mutation inventory")
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sort.Strings(lines)
			facts = append(facts, strings.Join(lines, "\n"))
		}
		return strings.Join(facts, "\n---\n")
	}
	events := func(since time.Time) []string {
		t.Helper()
		until := time.Now().Add(time.Second)
		format := "{{json .}}"
		if engine == "podman" {
			format = "json"
		}
		out, code := engineCall("events", "--since", since.UTC().Format(time.RFC3339Nano), "--until", until.UTC().Format(time.RFC3339Nano), "--format", format)
		if code != 0 {
			t.Fatal("engine event qualification unavailable")
		}
		d := json.NewDecoder(bytes.NewReader(out))
		actions := []string{}
		for {
			var event map[string]any
			if err := d.Decode(&event); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal("invalid engine event evidence")
			}
			action, _ := event["Action"].(string)
			if action == "" {
				action, _ = event["Status"].(string)
			}
			if action == "" {
				action, _ = event["status"].(string)
			}
			switch action {
			case "pull", "create", "start", "stop", "remove", "destroy", "kill", "connect", "disconnect":
				actions = append(actions, action)
			}
		}
		return actions
	}
	// Qualify that event capture can observe a real mutation (including image
	// pulls that preserve image IDs); do not rely on an always-empty stream.
	probeStart := time.Now()
	if _, code := engineCall("pull", "docker.io/library/alpine:3.20"); code != 0 {
		t.Fatal("event probe pull failed")
	}
	probe := events(probeStart)
	seenPull := false
	for _, action := range probe {
		seenPull = seenPull || action == "pull"
	}
	if !seenPull {
		t.Fatal("event backend did not observe qualification pull")
	}
	checkDenied := func(binding string) {
		t.Helper()
		old := snapshot()
		start := time.Now()
		out, code := dill(binding, "up", "--pull", "always")
		assertPublic(out)
		if code != 1 || !strings.Contains(string(out), "Secret configuration or source unavailable.") {
			t.Fatal("bad selected source did not safely block apply")
		}
		if snapshot() != old || inspect() != before || len(events(start)) != 0 {
			t.Fatal("unavailable source mutated engine")
		}
	}
	// Real owned orphan plus a desired recreate: failure must precede both.
	orphan := stack + "_orphan"
	if _, code := engineCall("run", "-d", "--name", orphan, "--label", "dill.managed=true", "--label", "dill.stack="+stack, "--label", "dill.service=orphan", "docker.io/library/alpine:3.20", "sleep", "600"); code != 0 {
		t.Fatal("cannot provision owned synthetic orphan")
	}
	if os.WriteFile(configPath, changedRecipe, 0600) != nil {
		t.Fatal("cannot provision desired recreate")
	}
	checkDenied(bindings[2])
	// Inject faults only into owned per-run synthetic fixtures, always restore.
	// Renaming the directory (not files) also proves no source open/read is used
	// by cleanup and that a missing parent blocks before pull/orphan/recreate.
	parked := sourceDir + "-parked"
	if err := os.Rename(sourceDir, parked); err != nil {
		t.Fatal("cannot inject missing fixture directory")
	}
	func() {
		defer func() {
			if os.Rename(parked, sourceDir) != nil {
				t.Fatal("cannot restore synthetic directory")
			}
		}()
		old := snapshot()
		start := time.Now()
		out, code := dill(bindings[1], "up", "--pull", "always")
		assertPublic(out)
		if code != 1 || snapshot() != old || len(events(start)) != 0 {
			t.Fatal("missing synthetic directory caused mutation")
		}
	}()
	// Source metadata and symlink failures are availability, never implicit
	// rotation. Privileged fixture ownership changes are provisioning only.
	chmodSource := func(mode string) {
		t.Helper()
		if _, code := call("sudo", "-n", "chmod", mode, source2); code != 0 {
			t.Fatal("cannot provision synthetic source mode fault")
		}
	}
	func() { chmodSource("0444"); defer chmodSource("0400"); checkDenied(bindings[1]) }()
	func() {
		savedSource := source2 + "-saved"
		if os.Rename(source2, savedSource) != nil {
			t.Fatal("cannot provision synthetic source rename")
		}
		defer func() {
			os.Remove(source2)
			if os.Rename(savedSource, source2) != nil {
				t.Fatal("cannot restore synthetic source")
			}
		}()
		if os.Symlink(savedSource, source2) != nil {
			t.Fatal("cannot provision symlink source fault")
		}
		checkDenied(bindings[1])
	}()
	if os.WriteFile(configPath, originalRecipe, 0600) != nil {
		t.Fatal("cannot restore synthetic recipe")
	}
	// A changed inspected mount setting must cause drift with unchanged labels.
	// Recreate this owned synthetic container using identical configuration and
	// labels, but deliberately make its secret mount writable.
	inspectJSON, code := engineCall("inspect", container)
	if code != 0 {
		t.Fatal("cannot prepare owned mount drift")
	}
	var rows []struct {
		Config struct{ Labels map[string]string }
	}
	if json.Unmarshal(inspectJSON, &rows) != nil || len(rows) != 1 {
		t.Fatal("invalid owned inspect")
	}
	if _, code := engineCall("rm", "-f", container); code != 0 {
		t.Fatal("cannot inject owned mount drift")
	}
	args := []string{"run", "-d", "--name", container, "--user", fmt.Sprintf("%d:%d", uid, gid), "--network", stack, "--mount", "type=bind,src=" + source2 + ",dst=/run/secrets/db"}
	for name, value := range rows[0].Config.Labels {
		args = append(args, "--label", name+"="+value)
	}
	args = append(args, "docker.io/library/alpine:3.20", "sleep", "600")
	if _, code := engineCall(args...); code != 0 {
		t.Fatal("cannot create synthetic writable mount")
	}
	assertPlan(bindings[1], 1, 1, 0)
	driftOut, driftCode := dill(bindings[1], "plan", "--format", "json")
	assertPublic(driftOut)
	if driftCode != 1 || !strings.Contains(string(driftOut), "secret_mounts") {
		t.Fatal("writable bind not detected privately")
	}
	out, code = dill(bindings[1], "up")
	assertPublic(out)
	if code != 0 {
		t.Fatal("checked up did not repair inspected mount drift")
	}
	readAs(uid, values[1])
	assertPlan(bindings[1], 0, 0, 1)
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
