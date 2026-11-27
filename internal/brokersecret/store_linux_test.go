//go:build linux

package brokersecret

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runtimeFixture(t *testing.T) (*Store, Envelope, []Grant, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("qualified runtime requires rootful Linux")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "runtime")
	if err := Provision(root); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e, g := envelopeFixture(t)
	return s, e, g, root
}
func TestMaterializationScopeExpiryAndActive(t *testing.T) {
	s, e, g, root := runtimeFixture(t)
	clock := time.Now().UTC()
	s.now = func() time.Time { return clock }
	r, err := s.Prepare(e, e.Scope, g)
	if err != nil {
		t.Fatal(err)
	}
	wrong := e.Scope
	wrong.Generation = strings.Repeat("d", 32)
	if _, err = s.Resolve(r.Handle, wrong); err == nil {
		t.Fatal("wrong scope")
	}
	m, err := s.Resolve(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	mount := m.Mounts()[0]
	raw, err := os.ReadFile(mount.HostPath)
	if err != nil || !bytes.Equal(raw, e.Items[0].Bytes()) {
		t.Fatal("materialization mismatch")
	}
	meta, err := os.ReadFile(filepath.Join(root, r.Handle, "manifest.json"))
	if err != nil || bytes.Contains(meta, e.Items[0].Bytes()) {
		t.Fatal("plaintext metadata")
	}
	if _, err = s.Activate(r.Handle, e.Scope); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(6 * time.Minute)
	if _, err = s.Resolve(r.Handle, e.Scope); err != nil {
		t.Fatal("active generation expired")
	}
	other, err := s.Prepare(e, e.Scope, g)
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(6 * time.Minute)
	if _, err = s.Resolve(other.Handle, e.Scope); err != ErrExpired {
		t.Fatal("unused generation not expired")
	}
}
func TestMaterializationTamperAndMissingProtection(t *testing.T) {
	s, e, g, root := runtimeFixture(t)
	r, err := s.Prepare(e, e.Scope, g)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Resolve(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	file := m.Mounts()[0].HostPath
	if err = os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("changed"), 0400); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(file, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(r.Handle, e.Scope); err == nil {
		t.Fatal("tamper accepted")
	}
	if err = os.Remove(filepath.Join(root, "key")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Prepare(e, e.Scope, g); err == nil {
		t.Fatal("missing key accepted")
	}
	s.Close()
	if _, err = Open(root); err == nil {
		t.Fatal("missing state recreated")
	}
	if err = Provision(root); err == nil {
		t.Fatal("used state reprovisioned")
	}
}
func TestMaterializationSymlinkRefused(t *testing.T) {
	s, e, g, _ := runtimeFixture(t)
	r, err := s.Prepare(e, e.Scope, g)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Resolve(r.Handle, e.Scope)
	if err != nil {
		t.Fatal(err)
	}
	file := m.Mounts()[0].HostPath
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("../manifest.json", file); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(r.Handle, e.Scope); err == nil {
		t.Fatal("symlink accepted")
	}
}
