package metricstransport

import (
	"os"
	"os/exec"
	"testing"
)

func TestExtendedACLRejected(t *testing.T) {
	cfg := certConfig(t, nil, nil)
	// Install a Darwin ACL which grants read despite owner-only mode bits.
	if err := exec.Command("/bin/chmod", "+a", "everyone allow read", cfg.TokenFile).Run(); err != nil {
		t.Fatal("cannot set up synthetic ACL fixture")
	}
	if info, err := os.Stat(cfg.TokenFile); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("synthetic ACL fixture did not retain owner-only mode")
	}
	if server, err := Prepare(cfg, hooksOK()); server != nil || err == nil {
		t.Fatal("extended ACL was accepted despite private mode bits")
	}
}
