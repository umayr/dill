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

func TestCatalogLookupSyncsIndependentlyBeforeTrustingVisibleRecord(t *testing.T) {
	parent := t.TempDir()
	root, records := filepath.Join(parent, "stages"), filepath.Join(parent, "records")
	for _, p := range []string{root, records} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writer, err := OpenCatalog(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := OpenCatalog(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	s := Snapshot{"a", []Entry{entry("a", "content")}}
	stage, err := writer.Receive(context.Background(), root, s, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(filepath.Base(stage), "snapshot-")
	waiting, release := make(chan struct{}), make(chan struct{})
	writer.syncFD = func(fd int) error {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT == unix.S_IFDIR {
			close(waiting)
			<-release
			return unix.EIO
		}
		return unix.Fsync(fd)
	}
	result := make(chan error, 1)
	go func() { result <- writer.RegisterReceived(context.Background(), id, s) }()
	<-waiting
	manifest, err := reader.Lookup(context.Background(), id)
	if err != nil || manifest.Entrypoint != "a" {
		close(release)
		t.Fatalf("lookup: %+v %v", manifest, err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrCatalogUnknown) {
		t.Fatalf("writer outcome: %v", err)
	}
}
