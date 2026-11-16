//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublicationAndCollisionCleanup(t *testing.T) {
	for _, collide := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "collision"}[collide], func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			s := Snapshot{"a", []Entry{entry("a", "test")}}
			p, err := receive(context.Background(), root, s, MaximumLimits(), func(fd int, from string, tofd int, to string) error {
				if !strings.HasPrefix(from, ".in-progress-") || !strings.HasPrefix(to, "snapshot-") {
					t.Fatal("incorrect names")
				}
				if _, err := os.Stat(filepath.Join(root, to)); !os.IsNotExist(err) {
					t.Fatal("published early")
				}
				b, err := os.ReadFile(filepath.Join(root, from, "a"))
				if err != nil || string(b) != "test" {
					t.Fatal("incomplete stage", err)
				}
				if collide {
					if err := unix.Mkdirat(tofd, to, 0700); err != nil {
						return err
					}
				}
				return renameNoReplace(fd, from, tofd, to)
			}, cleanup, unix.Fstatat, identity)
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			if collide {
				if err == nil || p != "" || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "snapshot-") {
					t.Fatalf("collision cleanup: %q %v %v", p, err, entries)
				}
				return
			}
			if err != nil || len(entries) != 1 || filepath.Base(p) != entries[0].Name() {
				t.Fatalf("publication: %q %v %v", p, err, entries)
			}
		})
	}
}
func TestPublicationFailureCleansStage(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	expected := errors.New("publication failure")
	_, err := receive(context.Background(), root, Snapshot{"a", []Entry{entry("a", "data")}}, MaximumLimits(), func(int, string, int, string) error { return expected }, cleanup, unix.Fstatat, identity)
	if !errors.Is(err, expected) {
		t.Fatal(err)
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatalf("partial stage: %v %v", entries, e)
	}
}
