//go:build integration && (linux || darwin)

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These are real executable/daemon tests, not substitutes for the coordinator's
// synthetic SDK tests. Only an independently provisioned disposable native host
// may set these selectors. No expected daemon identity is obtained from /info by
// the test itself. --coordination-source brine is a CALLER assertion, not proof
// that the external Brine application was run.
type alphaQualifiedFixture struct {
	bin, root, stack, cfg, state, socket, daemon, image, imageID string
	foreignContainer, foreignNetwork, foreignVolume              string
	foreignBefore                                                string
	seen                                                         map[string]bool
}

func newAlphaQualifiedFixture(t *testing.T) *alphaQualifiedFixture {
	t.Helper()
	if os.Getenv("DILL_TEST_ALPHA_COORDINATION") != "1" {
		t.Skip("requires independently provisioned disposable native coordination opt-in")
	}
	if runtime.GOOS != "linux" || integrationEnv(t).engine != "docker" {
		t.Fatal("qualified runtime requires native Linux Docker; no unsupported-host fallback")
	}
	stateRoot := os.Getenv("DILL_TEST_ALPHA_STATE_ROOT")
	daemon := os.Getenv("DILL_TEST_ALPHA_DAEMON_ID")
	socket := os.Getenv("DILL_ENGINE_SOCKET")
	if daemon == "" || !strings.HasPrefix(socket, "unix:///") || !filepath.IsAbs(stateRoot) || os.Getenv("DILL_TEST_ALPHA_NATIVE_LAYOUT") != "native-local-docker-v1" {
		t.Fatal("qualified test requires independently provisioned daemon identity, native-layout attestation, private state root and explicit socket")
	}
	info, err := os.Lstat(stateRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatal("qualified state root is not a pre-existing private directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		t.Fatal("qualified state root belongs to another operator")
	}
	state, err := os.MkdirTemp(stateRoot, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			return // Preserve failed private state; never invent successful recovery.
		}
		if err := os.RemoveAll(state); err != nil {
			t.Errorf("private synthetic state cleanup failed (%T)", err)
			return
		}
		if _, err := os.Lstat(state); !os.IsNotExist(err) {
			t.Error("private synthetic state cleanup did not converge")
		}
	})
	root := repoRoot(t)
	f := &alphaQualifiedFixture{bin: dillBinary(t, root), root: root,
		stack: fmt.Sprintf("dill-aq-%d", time.Now().UnixNano()),
		cfg:   filepath.Join(t.TempDir(), "compose.pkl"), state: state,
		socket: socket, daemon: daemon, seen: map[string]bool{}}
	t.Cleanup(func() { alphaVerifyScopedCleanup(t, "docker", f.stack) })
	if _, err := runEngineRaw(t, "docker", "image", "inspect", alphaPreparedImage); err != nil {
		// Explicit fixture preparation, never a pull within qualified execution.
		runEngine(t, "docker", "pull", alphaPreparedImage)
	}
	var images []struct {
		ID     string `json:"Id"`
		Config *struct {
			Volumes map[string]json.RawMessage `json:"Volumes"`
		} `json:"Config"`
	}
	if json.Unmarshal([]byte(runEngine(t, "docker", "image", "inspect", alphaPreparedImage)), &images) != nil || len(images) != 1 || images[0].ID == "" || images[0].Config == nil || len(images[0].Config.Volumes) != 0 {
		t.Fatal("prepared qualified image has unknown metadata or image VOLUME")
	}
	f.imageID, f.image = images[0].ID, "localhost/"+f.stack+":prepared"
	t.Cleanup(func() { alphaCleanupResource(t, "docker", "image", f.image) })
	runEngine(t, "docker", "tag", f.imageID, f.image)
	f.foreignContainer, f.foreignNetwork, f.foreignVolume = f.stack+"-foreign", f.stack+"-foreign-net", f.stack+"-foreign-data"
	t.Cleanup(func() {
		alphaCleanupResource(t, "docker", "container", f.foreignContainer)
		alphaCleanupResource(t, "docker", "network", f.foreignNetwork)
		alphaCleanupResource(t, "docker", "volume", f.foreignVolume)
	})
	runEngine(t, "docker", "network", "create", f.foreignNetwork)
	runEngine(t, "docker", "volume", "create", f.foreignVolume)
	runEngine(t, "docker", "run", "--rm", "--network", "none", "-v", f.foreignVolume+":/foreign", alphaPreparedImage, "sh", "-c", "printf synthetic-foreign-v1 > /foreign/value")
	runEngine(t, "docker", "run", "-d", "--name", f.foreignContainer, "--network", f.foreignNetwork, "-v", f.foreignVolume+":/foreign", alphaPreparedImage, "sleep", "300")
	f.foreignBefore = f.foreignSnapshot(t)
	// Registration order is intentional: inspect foreign state AFTER managed
	// stack/storage cleanup and BEFORE removing these test-owned foreign fixtures.
	t.Cleanup(func() { f.assertForeign(t) })
	t.Cleanup(func() { alphaCleanupResource(t, "docker", "volume", f.stack+"_data") })
	cleanupStack(t, f.bin, f.cfg, f.stack)
	f.write(t, f.image, `volumes = List("data:/data")`, false)
	return f
}

func (f *alphaQualifiedFixture) write(t *testing.T, image, extra string, other bool, declarations ...string) {
	t.Helper()
	writeAlphaPreparedConfig(t, f.cfg, f.root, "docker", f.stack, image, extra, other, declarations...)
	evaluateAlphaFixture(t, f.cfg)
}

func (f *alphaQualifiedFixture) foreignSnapshot(t *testing.T) string {
	t.Helper()
	return strings.Join([]string{
		runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}|{{json .Config.Labels}}|{{json .Mounts}}|{{json .NetworkSettings.Networks}}", f.foreignContainer),
		runEngine(t, "docker", "network", "inspect", f.foreignNetwork),
		runEngine(t, "docker", "volume", "inspect", f.foreignVolume),
		runEngine(t, "docker", "exec", f.foreignContainer, "cat", "/foreign/value"),
	}, "\n")
}

func (f *alphaQualifiedFixture) assertForeign(t *testing.T) {
	t.Helper()
	if f.foreignSnapshot(t) != f.foreignBefore {
		t.Fatal("qualified attempt/cleanup changed synthetic foreign identities, labels, state, mounts or data")
	}
}

func (f *alphaQualifiedFixture) argsAt(socket, operation, source string, extra ...string) []string {
	// Deliberately NO --alpha-disposable: qualified mode must enforce its OWN
	// complete pilot preflight, not borrow safety from a separate opt-in flag.
	args := []string{"operation", operation, "--contract", "dill.operation/1", "--engine", "docker", "--engine-socket", socket,
		"-p", f.stack, "-f", f.cfg, "--coordination-state-dir", f.state,
		"--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", f.daemon,
		"--coordination-source", source, "--timeout", "60s"}
	return append(args, extra...)
}

func (f *alphaQualifiedFixture) runAt(t *testing.T, socket, operation, source, input string, extra ...string) (alphaOperationResult, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.bin, f.argsAt(socket, operation, source, extra...)...)
	cmd.Stdin = strings.NewReader(input)
	data, err := cmd.Output()
	code := 0
	if err != nil {
		exited, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("qualified executable did not return a terminal receipt: %T", err)
		}
		code = exited.ExitCode()
	}
	result, err := decodeAlphaOperation(data)
	if err != nil {
		t.Fatalf("qualified terminal contract invalid (raw output withheld): %v", err)
	}
	if result.Operation != operation || f.seen[result.OperationID] {
		t.Fatal("qualified receipt operation mismatch or reused operation ID")
	}
	f.seen[result.OperationID] = true
	if result.Target != nil {
		if result.Target.Engine != "docker" || result.Target.EndpointDigest != alphaDigest(socket) || result.Stack != f.stack {
			t.Fatal("qualified receipt lost explicit selector/stack correlation")
		}
		if result.Target.QualifiedIdentityDigest != "" && result.Target.QualifiedIdentityDigest != alphaDigest("dill-local-docker-v1\x00"+f.daemon) {
			t.Fatal("qualified receipt identity differs from independently provisioned target")
		}
	}
	return result, code
}

func (f *alphaQualifiedFixture) run(t *testing.T, operation, source, input string, extra ...string) (alphaOperationResult, int) {
	t.Helper()
	return f.runAt(t, f.socket, operation, source, input, extra...)
}

func (f *alphaQualifiedFixture) seed(t *testing.T) {
	t.Helper()
	result, code := f.run(t, "up", "cli", "")
	if code != 0 || result.Outcome != "succeeded" || result.Effects != "complete" || result.Target == nil || result.Target.QualifiedIdentityDigest == "" {
		t.Fatal("qualified preparation did not create an attested disposable workload")
	}
	if image := runEngine(t, "docker", "inspect", "--format", "{{.Image}}|{{.Config.Image}}", f.stack+"_app"); image != f.imageID+"|"+f.imageID {
		t.Fatal("qualified create content OR actual create reference was not the inspected immutable ID")
	}
	f.assertForeign(t)
}

func (f *alphaQualifiedFixture) rejectNoWrites(t *testing.T, operation string, wantCode int, wantOutcome string, extra ...string) alphaOperationResult {
	t.Helper()
	before := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	result, code := f.run(t, operation, "cli", "", extra...)
	until := time.Now().UTC().Format(time.RFC3339Nano)
	if code != wantCode || result.Outcome != wantOutcome || result.Effects != "none" {
		t.Fatalf("qualified rejection did not fail before writes: exit/outcome/effects=%d/%s/%s", code, result.Outcome, result.Effects)
	}
	if events := runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}"); events != "" {
		t.Fatal("qualified rejection produced daemon mutation events")
	}
	if after := alphaResourceSnapshot(t, "docker"); after != before {
		t.Fatal("qualified rejection changed complete synthetic resource inventory/state")
	}
	f.assertForeign(t)
	return result
}

func TestAlphaQualifiedStandalonePreflight(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	volumeImage := "localhost/" + f.stack + ":declared-volume"
	alphaPrepareVolumeImage(t, "docker", f.imageID, f.stack, volumeImage)
	for _, tc := range []struct {
		name, image, extra string
		code               int
		options            []string
	}{
		{"bind", f.image, `volumes = List("/synthetic-unused:/data")`, 2, nil},
		{"capability", f.image, `capAdd = List("SYS_ADMIN")`, 2, nil},
		{"host-network", f.image, `networkMode = "host"`, 2, nil},
		{"device", f.image, `devices = List("/synthetic-device:/dev/synthetic")`, 2, nil},
		{"anonymous-volume", f.image, `volumes = List("/data")`, 2, nil},
		{"tmpfs", f.image, `volumes = List(new Mount { type = "tmpfs"; target = "/data" })`, 2, nil},
		{"external-volume", f.image, fmt.Sprintf(`volumes = List(new Mount { source = %q; target = "/data"; externalVolume = true })`, f.foreignVolume), 2, nil},
		{"explicit-volume-name", f.image, `volumes = List(new Mount { source = "data"; target = "/data"; name = "synthetic-explicit" })`, 2, nil},
		{"pull-intent", f.image, `volumes = List("data:/data")`, 2, []string{"--pull", "always"}},
		{"unavailable-metadata", "localhost/" + f.stack + ":never-prepared", `volumes = List("data:/data")`, 1, nil},
		{"image-volume", volumeImage, `volumes = List("data:/data")`, 1, nil},
		{"covered-image-volume", volumeImage, `volumes = List("data:/hidden")`, 1, nil},
		{"secret-grant", f.image, `secrets { new { name = "synthetic"; target = "/run/secrets/synthetic" } }`, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declarations := ""
			if tc.name == "secret-grant" {
				declarations = `secrets { new { name = "synthetic" } }`
			}
			f.write(t, tc.image, tc.extra, false, declarations)
			outcome := "failed"
			if tc.code == 2 {
				outcome = "invalid_input"
			}
			result := f.rejectNoWrites(t, "up", tc.code, outcome, tc.options...)
			if tc.name == "secret-grant" {
				assertAlphaSecretBoundary(t, result)
			}
		})
	}
	t.Run("retargeted-tag-rechecked", func(t *testing.T) {
		f.write(t, f.image, `volumes = List("data:/data")`, false)
		// Controlled retarget BETWEEN requests, not a fabricated internal hook.
		// seed proved the actual create reference was immutable A: retargeting
		// its former desired tag cannot change that live content. A new request
		// must nevertheless refresh metadata and deny newly tagged VOLUME B.
		runEngine(t, "docker", "tag", volumeImage, f.image)
		f.rejectNoWrites(t, "up", 1, "failed")
	})
}

func TestAlphaQualifiedForeignOwnership(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_COORDINATION") != "1" {
		t.Skip("requires independently provisioned disposable native coordination opt-in")
	}
	for _, kind := range []string{"network", "volume", "container"} {
		t.Run(kind, func(t *testing.T) {
			f := newAlphaQualifiedFixture(t)
			identity := f.stack
			if kind == "volume" {
				identity += "_data"
			} else if kind == "container" {
				identity += "_app"
			}
			t.Cleanup(func() { alphaCleanupResource(t, "docker", kind, identity) })
			switch kind {
			case "network":
				identity = runEngine(t, "docker", "network", "create", identity)
			case "volume":
				runEngine(t, "docker", "volume", "create", identity)
			case "container":
				identity = runEngine(t, "docker", "run", "-d", "--name", identity, f.imageID, "sleep", "300")
			}
			f.rejectNoWrites(t, "up", 1, "failed")
		})
	}
}

func TestAlphaQualifiedOrphanRejection(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.write(t, f.image, `volumes = List("data:/data")`, true)
	f.seed(t)
	other := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_other")
	f.write(t, f.image, `volumes = List("data:/data")`, false)
	for _, options := range [][]string{nil, {"--no-prune"}} {
		f.rejectNoWrites(t, "up", 2, "invalid_input", options...)
		if after := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_other"); after != other {
			t.Fatal("qualified orphan rejection changed omitted resource")
		}
	}
}

func TestAlphaQualifiedPodmanRejected(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	// The request is rejected BEFORE target qualification. This tests the actual
	// unsupported selector against the same audited disposable host endpoint;
	// it is NOT qualified Podman runtime support or replacement baseline parity.
	result := f.rejectNoWrites(t, "up", 2, "invalid_input", "--engine", "podman")
	if result.Target != nil || len(result.Errors) != 1 || result.Errors[0].Code != "UNSUPPORTED_COORDINATION" {
		t.Fatal("qualified Podman did not reject before target qualification")
	}
	entries, err := os.ReadDir(f.state)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsupported qualified Podman initialized state or fell back")
	}
}

func alphaQualifiedAxes(t *testing.T, result alphaOperationResult, code, wantCode int, outcome, effects string) {
	t.Helper()
	if code != wantCode || result.Outcome != outcome || result.Effects != effects || result.Target == nil || result.Target.QualifiedIdentityDigest == "" {
		t.Fatalf("attested terminal axes mismatch: exit/outcome/effects=%d/%s/%s", code, result.Outcome, result.Effects)
	}
	if code == 0 && outcome == "succeeded" && len(result.Errors) != 0 {
		t.Fatal("qualified success carried errors")
	}
}

func alphaAcknowledgeKnownPartial(t *testing.T, f *alphaQualifiedFixture, result alphaOperationResult) {
	t.Helper()
	record := alphaInspectCoordination(t, f)
	if record.OperationID != result.OperationID || record.State != "terminal" || record.Outcome != "failed" || record.Effects != "partial" || record.Source != "cli" {
		t.Fatal("known partial receipt and actual durable record differ")
	}
	// Inspect the actual completed container removal and retained owned storage
	// before explicitly consenting. Acknowledgement does NOT undo/replay down.
	if runEngine(t, "docker", "ps", "-aq", "--filter", "label=dill.stack="+f.stack) != "" {
		t.Fatal("partial down still has live captured resources")
	}
	if runEngine(t, "docker", "network", "inspect", "--format", `{{index .Labels "dill.stack"}}|{{index .Labels "dill.managed"}}`, f.stack) != f.stack+"|true" || runEngine(t, "docker", "volume", "inspect", "--format", `{{index .Labels "dill.stack"}}|{{index .Labels "dill.managed"}}`, f.stack+"_data") != f.stack+"|true" {
		t.Fatal("partial down did not retain the inspected owned network/volume")
	}
	alphaOfflineNoEngineEffects(t, f, func() {
		if _, code := alphaOfflineCoordination(t, f, "acknowledge", "--operation-id", result.OperationID, "--inspected"); code != 0 {
			t.Fatal("known partial outcome did not accept exact inspected acknowledgement")
		}
	})
	if acknowledged := alphaInspectCoordination(t, f); acknowledged.OperationID != result.OperationID || acknowledged.State != "acknowledged" {
		t.Fatal("known partial acknowledgement lost exact operation correlation")
	}
}

func TestAlphaQualifiedHandlers(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	for _, operation := range []string{"stop", "start", "restart", "stop"} {
		result, code := f.run(t, operation, "cli", "")
		alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
	}
	before := alphaResourceSnapshot(t, "docker")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	result, code := f.run(t, "rm", "cli", "n\n", "--attended")
	until := time.Now().UTC().Format(time.RFC3339Nano)
	alphaQualifiedAxes(t, result, code, 0, "cancelled", "none")
	if after := alphaResourceSnapshot(t, "docker"); after != before || runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}") != "" {
		t.Fatal("qualified declined confirmation wrote engine state")
	}
	result, code = f.run(t, "rm", "cli", "", "--yes")
	alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
	f.rejectNoWrites(t, "down", 1, "failed") // Network retention is read-only.
	f.seed(t)
	result, code = f.run(t, "down", "cli", "")
	// This supported handler removes the immutable captured container but
	// conservatively retains the network; partial known completion is NOT success.
	alphaQualifiedAxes(t, result, code, 1, "failed", "partial")
	if runEngine(t, "docker", "ps", "-aq", "--filter", "label=dill.stack="+f.stack) != "" {
		t.Fatal("qualified down left the captured owned container")
	}
	alphaAcknowledgeKnownPartial(t, f, result)
	f.rejectNoWrites(t, "teardown", 2, "invalid_input", "--yes")
	f.assertForeign(t)
}

func TestAlphaQualifiedDockerProfile(t *testing.T) {
	f := newAlphaQualifiedFixture(t)
	f.seed(t)
	original := runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_app")
	if !strings.HasSuffix(original, "|running") {
		t.Fatal("qualified preparation was not running")
	}
	runEngine(t, "docker", "exec", f.stack+"_app", "sh", "-c", "printf synthetic-qualified-v1 > /data/value")
	since := time.Now().UTC().Format(time.RFC3339Nano)
	result, code := f.run(t, "up", "cli", "")
	until := time.Now().UTC().Format(time.RFC3339Nano)
	alphaQualifiedAxes(t, result, code, 0, "succeeded", "complete")
	if result.ChangesDetected || runEngine(t, "docker", "inspect", "--format", "{{.Id}}|{{.State.Status}}", f.stack+"_app") != original {
		t.Fatal("qualified immutable repeat did not converge on the same running container")
	}
	if runEngine(t, "docker", "events", "--since", since, "--until", until, "--format", "{{json .}}") != "" {
		t.Fatal("qualified no-op up mutated the daemon")
	}
	if runEngine(t, "docker", "exec", f.stack+"_app", "cat", "/data/value") != "synthetic-qualified-v1" {
		t.Fatal("qualified convergence changed dedicated volume content")
	}
	result, code = f.run(t, "down", "cli", "")
	alphaQualifiedAxes(t, result, code, 1, "failed", "partial")
	if runEngine(t, "docker", "run", "--rm", "--network", "none", "-v", f.stack+"_data:/data:ro", f.imageID, "cat", "/data/value") != "synthetic-qualified-v1" {
		t.Fatal("qualified down lost retained dedicated data")
	}
	alphaAcknowledgeKnownPartial(t, f, result)
	f.assertForeign(t)
}

func TestAlphaQualifiedActualLivePreflight(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_COORDINATION") != "1" {
		t.Skip("requires independently provisioned disposable native coordination opt-in")
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"privileged", []string{"--privileged"}},
		{"capability", []string{"--cap-add", "SYS_ADMIN"}},
		{"device", []string{"--device", "/dev/null:/dev/synthetic-null"}},
		{"pid-host", []string{"--pid", "host"}},
		{"ipc-host", []string{"--ipc", "host"}},
		{"uts-host", []string{"--uts", "host"}},
		{"cgroup-host", []string{"--cgroupns", "host"}},
		{"userns-host", []string{"--userns", "host"}},
		{"explicit-host-tmpfs", []string{"--tmpfs", "/synthetic-tmpfs"}},
		{"old-covered-image-volume", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAlphaQualifiedFixture(t)
			// Intentional pre-existing synthetic fixture, before any qualified
			// caller. Safe desired config/ownership labels do NOT attest live host
			// settings or the old image that start/restart would actually execute.
			runEngine(t, "docker", "network", "create", "--label", "dill.managed=true", "--label", "dill.stack="+f.stack, f.stack)
			runEngine(t, "docker", "volume", "create", "--label", "dill.managed=true", "--label", "dill.stack="+f.stack, f.stack+"_data")
			liveImage, target, wantCode, outcome := f.imageID, "/data", 2, "invalid_input"
			var oldImageID string
			if tc.name == "old-covered-image-volume" {
				liveImage = "localhost/" + f.stack + ":old-volume"
				oldImageID = alphaPrepareVolumeImage(t, "docker", f.imageID, f.stack, liveImage)
				target, wantCode, outcome = "/hidden", 1, "failed"
				f.write(t, f.image, `volumes = List("data:/hidden")`, false)
			}
			args := []string{"run", "-d", "--name", f.stack + "_app", "--network", f.stack,
				"--label", "dill.managed=true", "--label", "dill.stack=" + f.stack,
				"--label", "dill.service=app", "-v", f.stack + "_data:" + target}
			args = append(args, tc.args...)
			args = append(args, liveImage, "sh", "-c", alphaPreparedCommand)
			liveID := f.stack + "_app"
			if oldImageID != "" {
				// Tag cleanup was registered before commit; the extra dangling
				// immutable image ID becomes known only after successful creation.
				t.Cleanup(func() { alphaCleanupResource(t, "docker", "image", oldImageID) })
				t.Cleanup(func() { alphaCleanupResource(t, "docker", "container", liveID) })
			}
			liveID = runEngine(t, "docker", args...)
			if oldImageID != "" {
				// Now the old create tag resolves to SAFE A but actual live content
				// remains bad B. Census must use immutable .Image, never this tag.
				runEngine(t, "docker", "tag", f.imageID, liveImage)
			}
			for _, operation := range []string{"up", "down", "stop", "start", "restart", "rm"} {
				t.Run(operation, func(t *testing.T) {
					var options []string
					if operation == "rm" {
						options = []string{"--yes"}
					}
					f.rejectNoWrites(t, operation, wantCode, outcome, options...)
				})
			}
		})
	}
}
