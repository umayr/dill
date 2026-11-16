//go:build linux || darwin

package projectsnapshot

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRenameNoReplace(t *testing.T) {
	root := t.TempDir()
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := os.Mkdir(filepath.Join(root, "in-progress"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "snapshot"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := renameNoReplace(fd, "in-progress", fd, "snapshot"); err == nil {
		t.Fatal("replaced existing stage")
	}
	for _, name := range []string{"in-progress", "snapshot"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := renameNoReplace(fd, "in-progress", fd, "published"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "published")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "in-progress")); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
}
