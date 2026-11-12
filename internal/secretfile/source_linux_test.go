//go:build linux

package secretfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestSourceMetadata(t *testing.T) {
	dir := protectedTestDir(t)
	var err error
	file := filepath.Join(dir, "source")
	if err = os.WriteFile(file, []byte("synthetic-sentinel"), 0400); err != nil {
		t.Fatal(err)
	}
	s := config.ResolvedSecret{HostPath: file, HostUID: int64(os.Geteuid()), HostGID: int64(os.Getegid()), HostMode: 0400}
	if _, err = checkSource(s); err != nil {
		fixtureDiagnostics(t, file)
		t.Fatalf("qualified synthetic file metadata rejected: %v", err)
	}

	s.UID = s.HostUID
	s.GID = s.HostGID
	s.Mode = s.HostMode
	mapping := Mapping{UID: []IDRange{{Container: uint64(s.UID), Host: uint64(s.HostUID), Size: 1}}, GID: []IDRange{{Container: uint64(s.GID), Host: uint64(s.HostGID), Size: 1}}}
	if err := Check(map[string][]config.ResolvedSecret{"one": {s}, "two": {s}}, mapping); err != nil {
		t.Fatalf("matching alias rejected: %v", err)
	}
	conflicting := s
	conflicting.Mode = 288
	if err := Check(map[string][]config.ResolvedSecret{"one": {s}, "two": {conflicting}}, mapping); !errors.Is(err, config.ErrSecretUnavailable) {
		t.Fatal("conflicting alias accepted")
	}
	if err = os.Symlink(file, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	bad := s
	bad.HostPath = filepath.Join(dir, "link")
	if _, err = checkSource(bad); !errors.Is(err, config.ErrSecretUnavailable) {
		t.Fatal("symlink accepted")
	}
	if err = os.Link(file, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err = checkSource(s); !errors.Is(err, config.ErrSecretUnavailable) {
		t.Fatal("hardlink accepted")
	}
	if err = os.Remove(filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(file, 0444); err != nil {
		t.Fatal(err)
	}
	if _, err = checkSource(s); !errors.Is(err, config.ErrSecretUnavailable) {
		t.Fatal("mode mismatch accepted")
	}
}
