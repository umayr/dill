//go:build linux

package secretfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func TestSourceMetadata(t *testing.T) {
	dir, err := os.MkdirTemp(".", "secretfile-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "source")
	if err = os.WriteFile(file, []byte("synthetic-sentinel"), 0400); err != nil {
		t.Fatal(err)
	}
	s := config.ResolvedSecret{HostPath: file, HostUID: int64(os.Geteuid()), HostGID: int64(os.Getegid()), HostMode: 0400}
	for parent := dir; ; parent = filepath.Dir(parent) {
		fd, e := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			t.Fatal(e)
		}
		var st unix.Stat_t
		e = unix.Fstat(fd, &st)
		unix.Close(fd)
		if e != nil {
			t.Fatal(e)
		}
		if st.Mode&0022 != 0 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
			t.Skip("test workspace is under an untrusted or writable ancestor")
		}
		if parent == "/" {
			break
		}
	}
	if _, err = checkSource(s); err != nil {
		t.Fatalf("valid file metadata rejected: %v", err)
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
