package projectsnapshot

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CI fixture is a dedicated 2 MiB tmpfs, never the runner's root filesystem.
func TestSmallFilesystemAdmissionRecovers(t *testing.T) {
	mount := os.Getenv("DILL_TEST_SMALL_TMPFS")
	if mount == "" {
		t.Skip("requires isolated small tmpfs")
	}
	var st unix.Statfs_t
	if err := unix.Statfs(mount, &st); err != nil {
		t.Fatal(err)
	}
	if st.Type != unix.TMPFS_MAGIC || st.Blocks*uint64(st.Bsize) > 2<<20 {
		t.Fatal("expected dedicated small tmpfs")
	}
	root, err := os.MkdirTemp(mount, "stages-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	stage := func(content string) (string, error) {
		return Receive(context.Background(), root, Snapshot{"compose.pkl", []Entry{entry("compose.pkl", content)}}, MaximumLimits())
	}
	first, err := stage("original")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stage(strings.Repeat("x", 3<<20)); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("expected ENOSPC, got %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 {
		t.Fatalf("partial stage retained: %v %v", files, err)
	}
	b, err := os.ReadFile(filepath.Join(first, "compose.pkl"))
	if err != nil || string(b) != "original" {
		t.Fatalf("prior snapshot lost: %v", err)
	}
	if _, err := stage("recovered"); err != nil {
		t.Fatal(err)
	}
}
