package agentcmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployGrantsRequireConfiguredBackend(t *testing.T) {
	for _, route := range httpsRoutes {
		if route.backend == httpsMonitor {
			continue
		}
		t.Run(route.grant, func(t *testing.T) {
			grants := httpsDeployGrants{route.grant: true}
			if checkDeployGrantBackends(grants, false, false, false) == nil {
				t.Fatal("missing backend accepted")
			}
			if checkDeployGrantBackends(grants, true, false, false) == nil && route.backend != httpsStaging {
				t.Fatal("staging expanded grant")
			}
			if checkDeployGrantBackends(grants, true, true, false) == nil && route.backend == httpsApply {
				t.Fatal("preview expanded apply authority")
			}
			minimumPreview := route.backend == httpsPreview || route.backend == httpsApply
			minimumApply := route.backend == httpsApply
			if e := checkDeployGrantBackends(grants, true, minimumPreview, minimumApply); e != nil {
				t.Fatal("minimum valid backend rejected", e)
			}
			if e := checkDeployGrantBackends(grants, true, true, true); e != nil {
				t.Fatal(e)
			}
		})
	}
	var diagnostics bytes.Buffer
	code := Main([]string{"--https-listen", "127.0.0.1:8443", "--https-staging-root", "/staging", "--https-staging-catalog", "/catalog", "--https-deploy-grant", "journal"}, io.Discard, &diagnostics, true)
	if code == 0 || !strings.Contains(diagnostics.String(), "grant requires its configured backend") {
		t.Fatal("startup did not reject unsatisfied grant before credentials/listener", code, diagnostics.String())
	}
}
func TestDeploymentHistoryRejectsDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	history := filepath.Join(root, "history")
	other := filepath.Join(root, "other")
	alias := filepath.Join(root, "alias")
	for _, path := range []string{history, other} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink(history, alias); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{history, history + "/", filepath.Join(history, ".") + "/.", alias} {
		if separateDeploymentHistory(history, path) {
			t.Errorf("shared directory accepted: %s", path)
		}
	}
	if separateDeploymentHistory(filepath.Join(root, "missing"), filepath.Join(root, "missing")+"/") {
		t.Fatal("absent history alias accepted")
	}
	if !separateDeploymentHistory(history, other) {
		t.Fatal("distinct directories refused")
	}
}
