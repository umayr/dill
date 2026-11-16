//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func catalogFixture(t *testing.T) (*Catalog, string, string, Snapshot, string) {
	t.Helper()
	root := t.TempDir()
	stages := filepath.Join(root, "stages")
	records := filepath.Join(root, "records")
	for _, p := range []string{stages, records} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	s := Snapshot{Entrypoint: "a/main.pkl", Files: []Entry{{Path: "a/main.pkl", Content: []byte("hello"), SHA256: sha256.Sum256([]byte("hello"))}, {Path: "other", Content: []byte("x"), SHA256: sha256.Sum256([]byte("x"))}}}
	path, e := Receive(context.Background(), stages, s, MaximumLimits())
	if e != nil {
		t.Fatal(e)
	}
	id := strings.TrimPrefix(filepath.Base(path), "snapshot-")
	c, e := OpenCatalog(stages, records)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c, stages, records, s, id
}
func TestCatalogLifecycleAndTamper(t *testing.T) {
	c, stages, records, s, id := catalogFixture(t)
	ctx := context.Background()
	if _, e := c.Lookup(ctx, id); !errors.Is(e, ErrStageUnknown) {
		t.Fatalf("unregistered stage: %v", e)
	}
	if e := c.RegisterReceived(ctx, id, s); e != nil {
		t.Fatal(e)
	}
	m, e := c.Lookup(ctx, id)
	if e != nil || m.Entrypoint != s.Entrypoint {
		t.Fatalf("lookup: %+v %v", m, e)
	}
	if e = os.WriteFile(filepath.Join(stages, "snapshot-"+id, "a/main.pkl"), []byte("HELLO"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Lookup(ctx, id); e == nil {
		t.Fatal("accepted corrupted stage")
	}
	if e = os.WriteFile(filepath.Join(records, id), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Lookup(ctx, id); e == nil {
		t.Fatal("accepted corrupt record")
	}
}
func TestCatalogForeignRootSwapAndSymlink(t *testing.T) {
	c, stages, _, s, id := catalogFixture(t)
	ctx := context.Background()
	foreign := strings.Repeat("a", 32)
	if e := os.Mkdir(filepath.Join(stages, "snapshot-"+foreign), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lookup(ctx, foreign); !errors.Is(e, ErrStageUnknown) {
		t.Fatalf("foreign: %v", e)
	}
	if e := c.RegisterReceived(ctx, id, s); e != nil {
		t.Fatal(e)
	}
	stage := filepath.Join(stages, "snapshot-"+id)
	if e := os.Rename(stage, stage+"-moved"); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(stage+"-moved", stage); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lookup(ctx, id); e == nil {
		t.Fatal("accepted symlink stage")
	}
	if e := os.Rename(stages, stages+"-old"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(stages, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lookup(ctx, id); e == nil {
		t.Fatal("accepted swapped root")
	}
}
func TestCatalogFailuresRetainAndNoRetry(t *testing.T) {
	for _, where := range []string{"file-sync", "rename", "dir-sync"} {
		t.Run(where, func(t *testing.T) {
			c, stages, records, s, id := catalogFixture(t)
			calls := 0
			syncReal := c.syncFD
			c.syncFD = func(fd int) error {
				calls++
				if where == "file-sync" && calls == 1 || where == "dir-sync" && calls == 2 {
					return errors.New("injected")
				}
				return syncReal(fd)
			}
			renameReal := c.rename
			c.rename = func(a int, b string, d int, e string) error {
				if where == "rename" {
					return errors.New("injected")
				}
				return renameReal(a, b, d, e)
			}
			err := c.RegisterReceived(context.Background(), id, s)
			if err == nil {
				t.Fatal("failure ignored")
			}
			if (where != "file-sync") != errors.Is(err, ErrCatalogUnknown) {
				t.Fatalf("outcome: %v", err)
			}
			if _, e := os.Stat(filepath.Join(stages, "snapshot-"+id)); e != nil {
				t.Fatal(e)
			}
			entries, e := os.ReadDir(records)
			if e != nil || len(entries) != 1 {
				t.Fatalf("record lost: %v %v", entries, e)
			}
		})
	}
}
func TestCatalogConcurrentAndCancellation(t *testing.T) {
	c, _, records, s, id := catalogFixture(t)
	ctx := context.Background()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e := c.RegisterReceived(cancelled, id, s); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var errs [8]error
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = c.RegisterReceived(ctx, id, s) }(i)
	}
	wg.Wait()
	success := 0
	for _, e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrCatalogUnknown) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("successes: %d", success)
	}
	if _, e := c.Lookup(ctx, id); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(filepath.Join(records, id), filepath.Join(records, "saved-record")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("missing", filepath.Join(records, id)); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lookup(ctx, id); e == nil || errors.Is(e, ErrStageUnknown) {
		t.Fatalf("symlink record: %v", e)
	}
}
func TestCatalogExtraEntry(t *testing.T) {
	c, stages, _, s, id := catalogFixture(t)
	ctx := context.Background()
	if e := c.RegisterReceived(ctx, id, s); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(stages, "snapshot-"+id, "extra")
	if e := os.Symlink("other", p); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Lookup(ctx, id); e == nil {
		t.Fatal("accepted extra symlink")
	}
}
func TestCatalogAmbiguousRenameActuallyPublished(t *testing.T) {
	c, _, _, s, id := catalogFixture(t)
	r := c.rename
	c.rename = func(a int, b string, d int, e string) error {
		if err := r(a, b, d, e); err != nil {
			return err
		}
		return unix.EIO
	}
	if e := c.RegisterReceived(context.Background(), id, s); !errors.Is(e, ErrCatalogUnknown) {
		t.Fatal(e)
	}
	if _, e := c.Lookup(context.Background(), id); e != nil {
		t.Fatal(e)
	}
}
