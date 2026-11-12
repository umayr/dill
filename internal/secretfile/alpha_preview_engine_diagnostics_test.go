//go:build integration && linux

package secretfile_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	logger "github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/secretfile"
)

// Read-only CI probe: capture the same four private HMAC frames immediately
// after the real CLI proof, verify their MAC, then emit only changed field paths.
// No engine mutation, polling, alternate proof baseline or production hook.
func TestQualifiedAlphaPreviewSnapshot(t *testing.T) {
	stage := os.Getenv("DILL_ALPHA_DIAG_PREVIEW_STAGE")
	if stage == "" {
		t.Skip("invoked only around the synthetic lifecycle assertion")
	}
	if os.Getenv("DILL_TEST_ALPHA_SECRETS") != "1" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DILL_ALPHA_ENGINE") != "podman" {
		t.Fatal("dedicated Podman CI required")
	}
	if stage != "before" && stage != "stopped" && stage != "restarted" {
		t.Fatal("unknown diagnostic stage")
	}
	root := os.Getenv("DILL_ALPHA_FIXTURE_ROOT")
	base := filepath.Dir(root)
	if (base != "/srv/dill-alpha-podman-rootful" && base != "/srv/dill-alpha-podman-rootless") || root != filepath.Join(base, "fixtures") || os.Getenv("DILL_ALPHA_SOCKET") != filepath.Join(base, "engine.sock") {
		t.Fatal("dedicated synthetic endpoint required")
	}
	path, binding, policy, stack := os.Getenv("DILL_ALPHA_DIAG_CONFIG"), os.Getenv("DILL_ALPHA_DIAG_BINDINGS"), os.Getenv("DILL_SECRET_POLICY"), os.Getenv("DILL_ALPHA_DIAG_STACK")
	for _, p := range []string{path, binding, policy} {
		if filepath.Clean(p) != p || !strings.HasPrefix(p, root+"/") {
			t.Fatal("synthetic fixture boundary required")
		}
	}
	keyDir, proof := os.Getenv("DILL_ALPHA_DIAG_PREVIEW_DIR"), os.Getenv("DILL_ALPHA_DIAG_PREVIEW_PROOF")
	if filepath.Clean(keyDir) != keyDir || filepath.Dir(keyDir) != root || !strings.HasPrefix(filepath.Base(keyDir), "keys-") || filepath.Clean(proof) != proof || filepath.Dir(proof) != keyDir || !strings.HasPrefix(filepath.Base(proof), "proof-") || !strings.HasSuffix(proof, ".json") {
		t.Fatal("private synthetic artifact boundary required")
	}
	if !strings.HasPrefix(stack, "alpha-") {
		t.Fatal("synthetic stack required")
	}
	phase := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("synthetic preview diagnostic phase %s failed", name)
		}
	}
	restore := logger.Suppress()
	defer restore()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := config.Load(config.WithPrivateEvaluation(ctx), path)
	phase("evaluate", err)
	if cfg.Name != stack || cfg.Engine != "podman" || len(cfg.Services) != 1 {
		t.Fatal("unexpected synthetic recipe")
	}
	svc := cfg.Services["app"]
	if svc == nil || svc.Image != "docker.io/library/alpine:3.20" || !reflect.DeepEqual(svc.Command, []string{"sleep", "600"}) || svc.User != fmt.Sprintf("%s:%s", os.Getenv("DILL_ALPHA_CONTAINER_UID"), os.Getenv("DILL_ALPHA_CONTAINER_GID")) || len(svc.Environment) != 0 || len(svc.Labels) != 0 || len(svc.Volumes) != 0 || len(svc.Ports) != 0 || svc.ContainerName != "" {
		t.Fatal("unexpected synthetic workload")
	}
	req := secretfile.Request{Config: cfg, PolicyPath: policy, BindingsPath: binding, Target: "lab", Stack: stack, Engine: "podman", Socket: os.Getenv("DILL_ALPHA_SOCKET")}
	checked, err := secretfile.Preflight(ctx, req)
	phase("preflight", err)
	mounts, err := checked.Mounts(req)
	phase("checked mounts", err)
	for _, rows := range mounts {
		for _, mount := range rows {
			if !strings.HasPrefix(mount.HostPath, root+"/") {
				t.Fatal("non-synthetic source")
			}
		}
	}
	engine, err := orchestrator.NewPodmanEngine(ctx, "unix://"+req.Socket)
	phase("connect", err)
	defer engine.Close()
	_, state, err := plan.ComputePreview(ctx, cfg, engine, stack, req, checked, plan.PreviewOptions{Timeout: 5 * time.Minute})
	phase("compute", err)
	baseline := ""
	if stage != "before" {
		baseline = filepath.Join(keyDir, "lifecycle-before.json")
	}
	matched, changed, err := secretfile.AlphaPreviewDiagnosticSnapshot(req, checked, state, filepath.Join(keyDir, "preview.key"), proof, filepath.Join(keyDir, "lifecycle-"+stage+".json"), baseline)
	phase("snapshot", err)
	t.Logf("synthetic preview stage=%s matches_cli_proof=%t changed_field_paths_bounded_64=%v", stage, matched, changed)
	if !matched {
		t.Fatal("fresh snapshot does not match CLI proof; do not interpret it as exact CLI evidence")
	}
}
