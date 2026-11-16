//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReceiveWithRootUsesAnchoredDirectory(t *testing.T) {
	parent := t.TempDir()
	root, original := filepath.Join(parent, "stages"), filepath.Join(parent, "old")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := os.Rename(root, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := receiveWithRoot(context.Background(), root, fd, Snapshot{"a", []Entry{entry("a", "content")}}, MaximumLimits(), renameNoReplace, cleanup, unix.Fstatat, identity, unix.Fsync)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "snapshot-") {
		t.Fatal(name)
	}
	if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
		t.Fatalf("published in replaced path: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(original, name, "a"))
	if err != nil || string(b) != "content" {
		t.Fatalf("anchored publication: %q %v", b, err)
	}
}

func TestCatalogReceiveRejectsReplacedRoot(t *testing.T) {
	parent := t.TempDir()
	root, records := filepath.Join(parent, "stages"), filepath.Join(parent, "records")
	for _, p := range []string{root, records} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c, err := OpenCatalog(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Receive(context.Background(), root, Snapshot{"a", []Entry{entry("a", "content")}}, MaximumLimits()); err == nil {
		t.Fatal("replaced root accepted")
	}
	for _, p := range []string{root, root + "-old", records} {
		entries, err := os.ReadDir(p)
		if err != nil || len(entries) != 0 {
			t.Fatalf("unexpected publication: %s %v %v", p, entries, err)
		}
	}
}
