//go:build integration

package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/secretfile"
)

func alphaSyntheticDiagnostic(text string) string {
	for _, value := range []string{"dill-alpha-synthetic-v1", "dill-alpha-synthetic-v2"} {
		sum := sha256.Sum256([]byte(value))
		if strings.Contains(text, value) || strings.Contains(text, hex.EncodeToString(sum[:])) {
			return "[synthetic source diagnostic withheld]"
		}
	}
	if len(text) > 8192 {
		text = text[:8192] + " [truncated]"
	}
	return text
}
func TestAlphaSyntheticDiagnosticFilter(t *testing.T) {
	value := "dill-alpha-synthetic-v1"
	sum := sha256.Sum256([]byte(value))
	for _, text := range []string{value, hex.EncodeToString(sum[:]), strings.Repeat("x", 9000) + value} {
		if alphaSyntheticDiagnostic(text) != "[synthetic source diagnostic withheld]" {
			t.Fatal("fixture diagnostic filter leaked")
		}
	}
	if len(alphaSyntheticDiagnostic(strings.Repeat("x", 9000))) > 8300 {
		t.Fatal("unbounded diagnostic")
	}
}

// CI-only failure probe, never a substitute for the real CLI qualification.
// Replay the same adapter phases against this run's already-qualified synthetic
// fixture to obtain the native SDK cause hidden by production's fixed errors.
// No production hook, alternate mount spec, proxy or relaxed preflight is used.
func TestQualifiedAlphaCreateDiagnostics(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_SECRETS") != "1" {
		t.Skip("disposable Alpha CI opt-in only")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DILL_ALPHA_ENGINE") != "podman" {
		t.Fatal("dedicated Podman CI required")
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
	if !strings.HasPrefix(stack, "alpha-") {
		t.Fatal("synthetic stack required")
	}
	phase := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("synthetic checked adapter phase %s: %s", name, alphaSyntheticDiagnostic(err.Error()))
		}
		t.Logf("synthetic checked adapter phase %s: passed", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
		for _, m := range rows {
			if !strings.HasPrefix(m.HostPath, root+"/") {
				t.Fatal("non-synthetic source")
			}
		}
	}
	p, err := NewPodmanEngine(ctx, "unix://"+req.Socket)
	phase("connect", err)
	defer p.Close()
	names, err := p.ListStack(ctx, stack)
	phase("ownership census", err)
	for _, name := range names {
		live, err := p.InspectConfig(ctx, name)
		phase("inspect ownership", err)
		phase("ownership", CheckOwnership(name, stack, live.SystemLabels["dill.service"], live))
	}
	name := stack + "_app"
	if live, err := p.InspectConfig(ctx, name); err == nil {
		phase("retained ownership", CheckOwnership(name, stack, "app", live))
		data, err := containers.Inspect(p.operationContext(ctx), name, nil)
		phase("retained inspect", err)
		if data.State != nil {
			t.Logf("synthetic retained container state=%s cause=%s", data.State.Status, alphaSyntheticDiagnostic(data.State.Error))
		}
		return
	} else if err != ErrNotFound {
		phase("desired inspect", err)
	}
	exists, err := p.ImageExists(ctx, svc.Image)
	phase("image exists", err)
	if !exists {
		phase("pull", p.PullImage(ctx, svc.Image, io.Discard))
	}
	phase("network", p.EnsureNetwork(ctx, stack))
	// Repeat precisely the checked creator's pre-create check; only test code
	// calls the private implementation to report its native cause unnormalized.
	selected, err := checkedSecretMounts(ctx, "app", svc, stack, "podman", p.socket, req)
	phase("pre-create preflight", err)
	_, err = p.startService(ctx, "app", svc, stack, selected, req.Target)
	phase("native create/start", err)
}
