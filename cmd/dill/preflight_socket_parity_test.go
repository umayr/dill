package main

import (
	"strings"
	"testing"

	"github.com/umayr/dill/internal/preflight"
)

func namedSocketArgs(socket string, apply bool) []string {
	contract := preflight.Version
	if apply {
		contract = "dill.operation/1"
	}
	args := []string{"start", "--contract", contract, "-f", "/synthetic/compose.pkl", "-p", "synthetic", "--engine", "docker", "--engine-socket", socket, "--coordination-layout", "native-local-docker-v1", "--coordination-daemon-id", "synthetic-only", "--coordination-state-dir", "/synthetic/state", "--coordination-source", "brine"}
	if apply {
		args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", "sha256:"+strings.Repeat("a", 64))
	}
	return args
}

func checkNamedSocketParity(t *testing.T, socket string) {
	t.Helper()
	_, existingErr := localEngineSocket(commandOptions{EngineSocket: socket, EngineSocketSet: true})
	for _, apply := range []bool{false, true} {
		_, code := preflight.ParseInvocation(namedSocketArgs(socket, apply), apply)
		if (code == "") != (existingErr == nil) || (code != "" && code != "INVALID_TARGET") {
			t.Fatalf("socket grammar differs: %q apply=%v existing=%v named=%s", socket, apply, existingErr, code)
		}
	}
}

func TestNamedSocketGrammarMatchesExplicitSelector(t *testing.T) {
	for _, socket := range []string{
		"/run/x.sock", "unix:///run/x.sock", "/run/with space.sock", "/run/é.sock", "/run//x.sock", "/",
		"", "relative", " /run/x", "unix:/run/x", "unix://authority/run/x", "unix://user@/run/x", "unix://user:password@authority/run/x", "unix:opaque", "UNIX:///run/x",
		"/run/x?query", "unix:///run/x#frag", "/run/x%2fsuffix", "/run/x\\suffix", "//run/x", "unix:////run/x",
		"/run/x\t", "/run/\tx", "/run/x\x00", "/run/x\x7f", "/run/x\u0085", "/run/x\xff", "/run/x ", "/run/x\u00a0", "/run/x\u2003",
	} {
		checkNamedSocketParity(t, socket)
	}
}

func FuzzNamedSocketGrammarParity(f *testing.F) {
	for _, seed := range []string{"", "/run/x.sock", "unix:///run/x.sock", "/run/x?query", "//run/x", "/run/x\xff", "/run/x\u00a0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, socket string) { checkNamedSocketParity(t, socket) })
}
