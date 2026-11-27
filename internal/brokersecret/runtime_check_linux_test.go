//go:build linux

package brokersecret

import (
	"os"
	"testing"
)

func TestRuntimeVerifierReceivesOnlyFreshAuthenticatedPaths(t *testing.T) {
	s, e, g, root := runtimeFixture(t)
	r, err := s.Prepare(e, e.Scope, g)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Resolve(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = m.CheckRuntime(func(path string, mounts []Mount) error {
		called = true
		if path != root || len(mounts) != len(g) || mounts[0].HostPath != m.Mounts()[0].HostPath {
			t.Fatal("unbound runtime metadata")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatal(err)
	}
	if err = m.CheckRuntime(func(string, []Mount) error { return ErrUnavailable }); err == nil {
		t.Fatal("runtime verifier refusal ignored")
	}
	if err = os.Chmod(m.Mounts()[0].HostPath, 0600); err != nil {
		t.Fatal(err)
	}
	called = false
	if err = m.CheckRuntime(func(string, []Mount) error { called = true; return nil }); err == nil || called {
		t.Fatal("tampered generation reached runtime verifier")
	}
}
