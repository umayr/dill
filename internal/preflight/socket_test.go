package preflight

import "testing"

func TestNamedSocketRejectsExistingSelectorInvalidSpellings(t *testing.T) {
	rows, suffix := invocationFixtures(t)
	bad := []string{
		"/run/x?query", "unix:///run/x#frag", "/run/x%2fsuffix", "/run/x\\suffix", "//run/x",
		"/run/x\t", "/run/\tx", "/run/x\x7f", "/run/x\u0085", "/run/x\xff", "/run/x ", "/run/x\u00a0",
		"unix://authority/run/x", "unix://user@/run/x", "unix:/run/x", "unix:////run/x", "UNIX:///run/x",
	}
	for _, socket := range bad {
		t.Run(socket, func(t *testing.T) {
			preview := replaceValue(rows[0], "--engine-socket", socket)
			if _, code := ParseInvocation(preview, false); code != "INVALID_TARGET" {
				t.Errorf("preview accepted invalid explicit selector: %q code=%s", socket, code)
			}
			apply := append(replaceValue(preview, "--contract", "dill.operation/1"), suffix...)
			if _, code := ParseInvocation(apply, true); code != "INVALID_TARGET" {
				t.Errorf("bound apply accepted invalid explicit selector: %q code=%s", socket, code)
			}
		})
	}
	for _, socket := range []string{"/run/x.sock", "unix:///run/x.sock", "/run/with space.sock", "/run/é.sock", "/run//x.sock"} {
		if _, code := ParseInvocation(replaceValue(rows[0], "--engine-socket", socket), false); code != "" {
			t.Errorf("documented explicit selector rejected: %q code=%s", socket, code)
		}
	}
}
