package plan

import (
	"errors"
	"fmt"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// --- diffPorts ---

func pb(published, target, proto, hostIP string) config.PortBinding {
	return config.PortBinding{Published: published, Target: target, Protocol: proto, HostIP: hostIP}
}

func TestDiffPorts_noDiff(t *testing.T) {
	desired := []config.PortBinding{pb("8080", "80", "tcp", "")}
	live := []config.PortBinding{pb("8080", "80", "tcp", "")}
	if diffs := diffPorts(desired, live); len(diffs) != 0 {
		t.Errorf("expected no diffs, got %+v", diffs)
	}
}

func TestDiffPorts_added(t *testing.T) {
	desired := []config.PortBinding{pb("8080", "80", "tcp", "")}
	live := []config.PortBinding{}
	diffs := diffPorts(desired, live)
	if len(diffs) != 1 || diffs[0].Before != "" {
		t.Errorf("expected 1 add diff, got %+v", diffs)
	}
}

func TestDiffPorts_removed(t *testing.T) {
	desired := []config.PortBinding{}
	live := []config.PortBinding{pb("8080", "80", "tcp", "")}
	diffs := diffPorts(desired, live)
	if len(diffs) != 1 || diffs[0].After != "" {
		t.Errorf("expected 1 remove diff, got %+v", diffs)
	}
}

func TestDiffPorts_changed(t *testing.T) {
	desired := []config.PortBinding{pb("9090", "80", "tcp", "")}
	live := []config.PortBinding{pb("8080", "80", "tcp", "")}
	diffs := diffPorts(desired, live)
	if len(diffs) != 1 {
		t.Errorf("expected 1 change diff, got %+v", diffs)
	}
}

// Host IP normalisation: "" should equal "0.0.0.0" and "::"
func TestDiffPorts_hostIPNormalization(t *testing.T) {
	desired := []config.PortBinding{pb("8080", "80", "tcp", "")}
	live := []config.PortBinding{pb("8080", "80", "tcp", "0.0.0.0")}
	if diffs := diffPorts(desired, live); len(diffs) != 0 {
		t.Errorf("expected no diffs for 0.0.0.0 vs empty host, got %+v", diffs)
	}

	live2 := []config.PortBinding{pb("8080", "80", "tcp", "::")}
	if diffs := diffPorts(desired, live2); len(diffs) != 0 {
		t.Errorf("expected no diffs for :: vs empty host, got %+v", diffs)
	}
}

// Image EXPOSE ports (no Published) should not be reported as removed.
func TestDiffPorts_imageExposeNotReported(t *testing.T) {
	desired := []config.PortBinding{}
	live := []config.PortBinding{pb("", "80", "tcp", "")} // no host binding
	if diffs := diffPorts(desired, live); len(diffs) != 0 {
		t.Errorf("expected no diffs for image EXPOSE port, got %+v", diffs)
	}
}

// --- diffVolumes ---

func vm(t, src, target string, ro bool) config.VolumeMount {
	return config.VolumeMount{Type: t, Source: src, Target: target, ReadOnly: ro}
}

func TestDiffVolumes_noDiff(t *testing.T) {
	desired := []config.VolumeMount{vm("volume", "mydata", "/data", false)}
	live := []config.VolumeMount{vm("volume", "mydata", "/data", false)}
	if diffs := diffVolumes(desired, live); len(diffs) != 0 {
		t.Errorf("expected no diffs, got %+v", diffs)
	}
}

func TestDiffVolumes_added(t *testing.T) {
	desired := []config.VolumeMount{vm("volume", "mydata", "/data", false)}
	live := []config.VolumeMount{}
	diffs := diffVolumes(desired, live)
	if len(diffs) != 1 || diffs[0].Before != "" {
		t.Errorf("expected 1 add diff, got %+v", diffs)
	}
}

func TestDiffVolumes_typeChanged(t *testing.T) {
	desired := []config.VolumeMount{vm("bind", "/host", "/data", false)}
	live := []config.VolumeMount{vm("volume", "mydata", "/data", false)}
	diffs := diffVolumes(desired, live)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for type change, got %+v", diffs)
	}
}

// --- normalizeRestart ---

func TestNormalizeRestart(t *testing.T) {
	if normalizeRestart("") != "no" {
		t.Error("empty should normalize to 'no'")
	}
	if normalizeRestart("no") != "no" {
		t.Error("'no' should normalize to 'no'")
	}
	if normalizeRestart("always") != "always" {
		t.Error("'always' should be unchanged")
	}
	if normalizeRestart("unless-stopped") != "unless-stopped" {
		t.Error("'unless-stopped' should be unchanged")
	}
}

// --- normalizeNetworkMode ---

func TestNormalizeNetworkMode(t *testing.T) {
	if normalizeNetworkMode("") != "" {
		t.Error("empty should normalize to empty")
	}
	if normalizeNetworkMode("bridge") != "" {
		t.Error("'bridge' should normalize to empty")
	}
	if normalizeNetworkMode("default") != "" {
		t.Error("'default' should normalize to empty")
	}
	if normalizeNetworkMode("host") != "host" {
		t.Error("'host' should be unchanged")
	}
}

func TestDiffConfigs_redactsSensitiveEnv(t *testing.T) {
	svc := &config.Service{
		Image: "busybox:1.36",
		Environment: map[string]string{
			"DB_PASSWORD": "new-secret",
			"PUBLIC":      "new-value",
		},
	}
	live := &orchestrator.LiveConfig{
		Image: "busybox:1.36",
		Env: map[string]string{
			"DB_PASSWORD": "old-secret",
			"PUBLIC":      "old-value",
		},
	}
	diffs, err := diffConfigs("web", svc, live)
	if err != nil {
		t.Fatal(err)
	}
	seenSecret := false
	seenPublic := false
	for _, d := range diffs {
		switch d.Field {
		case "env.DB_PASSWORD":
			seenSecret = true
			if d.Before != "[redacted]" || d.After != "[redacted]" {
				t.Fatalf("secret diff not redacted: %+v", d)
			}
		case "env.PUBLIC":
			seenPublic = true
			if d.Before != "old-value" || d.After != "new-value" {
				t.Fatalf("public diff unexpectedly changed: %+v", d)
			}
		}
	}
	if !seenSecret || !seenPublic {
		t.Fatalf("missing expected diffs: %+v", diffs)
	}
}

// --- isNotFound ---

func TestIsNotFound(t *testing.T) {
	err := fmt.Errorf("%w: mycontainer", orchestrator.ErrNotFound)
	if !isNotFound(err) {
		t.Error("expected isNotFound=true for wrapped ErrNotFound")
	}
	if isNotFound(errors.New("some other error")) {
		t.Error("expected isNotFound=false for unrelated error")
	}
	if isNotFound(nil) {
		t.Error("expected isNotFound=false for nil")
	}
}
