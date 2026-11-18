package agentcmd

import (
	"strings"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func TestRemotePreviewAndApplyBindIdenticalFlags(t *testing.T) {
	for _, action := range []string{"up", "down"} {
		t.Run(action, func(t *testing.T) {
			const path, stack, socket, daemon, state = "/stages/abc/compose.pkl", "demo", "unix:///run/docker.sock", "test-daemon", "/private/state"
			selection := "sha256:" + strings.Repeat("f", 64)
			preview := deploymentCLIArgs(action, path, stack, socket, daemon, state, "")
			apply := deploymentCLIArgs(action, path, stack, socket, daemon, state, selection)
			if preview[0] != "preflight" || apply[0] != "_managed-operation" {
				t.Fatal("incorrect command dispatch")
			}
			p, preflightErr := preflight.ParseInvocation(preview[1:], false)
			a, applyErr := preflight.ParseInvocation(apply[1:], true)
			if preflightErr != "" || applyErr != "" {
				t.Fatalf("invalid invocation: preview=%s apply=%s", preflightErr, applyErr)
			}
			if p.EffectiveFlags() != a.EffectiveFlags() {
				t.Fatalf("different digest inputs: preview=%+v apply=%+v", p.EffectiveFlags(), a.EffectiveFlags())
			}
			if a.ExpectedPreflight != selection {
				t.Fatal("selection digest not bound")
			}
			a.ExpectedPreflight = ""
			if p != a {
				t.Fatalf("preview and apply disagree: %v / %v", p, a)
			}
		})
	}
}
