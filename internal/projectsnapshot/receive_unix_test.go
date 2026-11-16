//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type cancelAfter struct {
	calls int
	after int
}

func (c *cancelAfter) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfter) Done() <-chan struct{}       { return nil }
func (c *cancelAfter) Value(any) any               { return nil }
func (c *cancelAfter) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}
func TestCancellationCleansStage(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s := Snapshot{"a/b", []Entry{entry("a/b", "data")}}
	ctx := &cancelAfter{after: 4}
	_, err := Receive(ctx, root, s, MaximumLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatalf("partial stage remains: %v %v", entries, e)
	}
}
func TestRejectSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	_, err := Receive(context.Background(), link, Snapshot{"x", []Entry{entry("x", "a")}}, MaximumLimits())
	if err == nil {
		t.Fatal("followed symlink")
	}
}
func TestCleanupRefusesReplacement(t *testing.T) {
	root := t.TempDir()
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := unix.Openat(fd, "x", unix.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	dev, ino, err := identity(f)
	unix.Close(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "x"), filepath.Join(root, "original")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "x")); err != nil {
		t.Fatal(err)
	}
	if err := cleanup([]created{{fd, "x", -1, dev, ino, false}}); err == nil {
		t.Fatal("removed replacement")
	}
	if _, err := os.Lstat(filepath.Join(root, "x")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "safe" {
		t.Fatal("outside changed")
	}
}
