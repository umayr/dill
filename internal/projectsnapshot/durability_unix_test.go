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

func TestAmbiguousRenameRetainsPublishedStage(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := receiveWithSync(context.Background(), root, Snapshot{"sub/a", []Entry{entry("sub/a", "content")}}, MaximumLimits(), func(a int, b string, c int, d string) error {
		if e := renameNoReplace(a, b, c, d); e != nil {
			return e
		}
		return errors.New("ambiguous rename result")
	}, cleanup, unix.Fstatat, identity, unix.Fsync)
	entries, readErr := os.ReadDir(root)
	if !errors.Is(err, ErrPublicationUnknown) || path != "" || readErr != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "snapshot-") {
		t.Fatalf("ambiguous rename: path=%q err=%v entries=%v read=%v", path, err, entries, readErr)
	}
	data, readErr := os.ReadFile(filepath.Join(root, entries[0].Name(), "sub", "a"))
	if readErr != nil || string(data) != "content" {
		t.Fatalf("published content lost: %q %v", data, readErr)
	}
}

func TestUnknownRenameIdentityRetainsInProgressStage(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	attempted := false
	_, err := receiveWithSync(context.Background(), root, Snapshot{"a", []Entry{entry("a", "content")}}, MaximumLimits(), func(int, string, int, string) error {
		attempted = true
		return errors.New("ambiguous rename")
	}, cleanup, func(fd int, name string, st *unix.Stat_t, flags int) error {
		if attempted {
			return unix.EIO
		}
		return unix.Fstatat(fd, name, st, flags)
	}, identity, unix.Fsync)
	entries, readErr := os.ReadDir(root)
	if !errors.Is(err, ErrPublicationUnknown) || readErr != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".in-progress-") {
		t.Fatalf("unknown identity: err=%v entries=%v read=%v", err, entries, readErr)
	}
	data, readErr := os.ReadFile(filepath.Join(root, entries[0].Name(), "a"))
	if readErr != nil || string(data) != "content" {
		t.Fatalf("stage lost: %q %v", data, readErr)
	}
}

func TestDurablePublicationSyncOrderAndFailure(t *testing.T) {
	for _, failure := range []string{"", "file", "directory", "root"} {
		t.Run(map[string]string{"": "success", "file": "file sync", "directory": "directory sync", "root": "root sync"}[failure], func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			snapshot := Snapshot{"sub/a", []Entry{entry("sub/a", "content")}}
			var steps []string
			published := false
			expected := errors.New("injected sync failure")
			path, err := receiveWithSync(context.Background(), root, snapshot, MaximumLimits(), func(a int, b string, c int, d string) error {
				steps = append(steps, "publish")
				e := renameNoReplace(a, b, c, d)
				if e == nil {
					published = true
				}
				return e
			}, cleanup, unix.Fstatat, identity, func(fd int) error {
				var st unix.Stat_t
				if e := unix.Fstat(fd, &st); e != nil {
					return e
				}
				kind := "directory"
				if st.Mode&unix.S_IFMT == unix.S_IFREG {
					kind = "file"
				} else if published {
					kind = "root"
				}
				steps = append(steps, kind)
				if kind == failure {
					return expected
				}
				return unix.Fsync(fd)
			})
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			switch failure {
			case "":
				if err != nil || len(entries) != 1 || filepath.Base(path) != entries[0].Name() {
					t.Fatalf("success: path=%q err=%v entries=%v", path, err, entries)
				}
				if got := strings.Join(steps, ","); got != "file,directory,directory,publish,root" {
					t.Fatalf("sync order %q", got)
				}
			case "root":
				if !errors.Is(err, ErrPublicationUnknown) || path != "" || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "snapshot-") {
					t.Fatalf("unknown publication: path=%q err=%v entries=%v", path, err, entries)
				}
				data, e := os.ReadFile(filepath.Join(root, entries[0].Name(), "sub", "a"))
				if e != nil || string(data) != "content" {
					t.Fatalf("published contents lost: %q %v", data, e)
				}
			default:
				if !errors.Is(err, expected) || path != "" || len(entries) != 0 || published {
					t.Fatalf("pre-publication cleanup: path=%q err=%v entries=%v", path, err, entries)
				}
			}
		})
	}
}
