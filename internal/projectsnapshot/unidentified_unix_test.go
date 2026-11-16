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

func TestUnidentifiedCreatedEntriesRequireInspection(t *testing.T) {
	for _, kind := range []string{"root", "nested", "file"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("private sentinel")
			calls := 0
			stat := func(fd int, name string, st *unix.Stat_t, flags int) error {
				calls++
				if (kind == "root" && calls == 1) || (kind == "nested" && calls == 2) {
					cancel()
					return errors.Join(ctx.Err(), failure)
				}
				return unix.Fstatat(fd, name, st, flags)
			}
			fileID := func(fd int) (uint64, uint64, error) {
				if kind == "file" {
					cancel()
					return 0, 0, errors.Join(ctx.Err(), failure)
				}
				return identity(fd)
			}
			_, err := receive(ctx, root, Snapshot{"dir/secret", []Entry{entry("dir/secret", "contents")}}, MaximumLimits(), renameNoReplace, cleanup, stat, fileID)
			var ce *CleanupError
			if !errors.As(err, &ce) || !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
				t.Fatalf("failure not classified: %v", err)
			}
			es, e := os.ReadDir(root)
			if e != nil || len(es) != 1 || !strings.HasPrefix(es[0].Name(), ".in-progress-") {
				t.Fatalf("unverified entry removed: %v %v", es, e)
			}
			stage := filepath.Join(root, es[0].Name())
			if kind == "nested" {
				if _, e = os.Lstat(filepath.Join(stage, "dir")); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "file" {
				if _, e = os.Lstat(filepath.Join(stage, "dir", "secret")); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
