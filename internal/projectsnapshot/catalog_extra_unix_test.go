//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCatalogCancellationAfterRenameIsUnknown(t *testing.T) {
	c, _, _, s, id := catalogFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	real := c.rename
	c.rename = func(a int, b string, d int, e string) error { err := real(a, b, d, e); cancel(); return err }
	if err := c.RegisterReceived(ctx, id, s); !errors.Is(err, ErrCatalogUnknown) {
		t.Fatalf("outcome: %v", err)
	}
	if _, err := c.Lookup(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}
func TestCatalogSeparateInstancesNoReplace(t *testing.T) {
	c, stages, records, s, id := catalogFixture(t)
	other, err := OpenCatalog(stages, records)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var a, b error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a = c.RegisterReceived(context.Background(), id, s) }()
	go func() { defer wg.Done(); b = other.RegisterReceived(context.Background(), id, s) }()
	wg.Wait()
	if !((a == nil && errors.Is(b, ErrCatalogUnknown)) || (b == nil && errors.Is(a, ErrCatalogUnknown))) {
		t.Fatalf("results: %v %v", a, b)
	}
}
func TestCatalogRecordRootSwap(t *testing.T) {
	c, _, records, s, id := catalogFixture(t)
	if err := c.RegisterReceived(context.Background(), id, s); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(records, records+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(records, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lookup(context.Background(), id); err == nil {
		t.Fatal("accepted swapped catalog root")
	}
}
func TestCatalogSymlinkFileAndMissingStage(t *testing.T) {
	c, stages, _, s, id := catalogFixture(t)
	if err := c.RegisterReceived(context.Background(), id, s); err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(stages, "snapshot-"+id)
	file := filepath.Join(stage, "other")
	if err := os.Rename(file, file+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file+"-old", file); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lookup(context.Background(), id); err == nil {
		t.Fatal("accepted symlink file")
	}
	if err := os.Rename(stage, stage+"-old"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Lookup(context.Background(), id); err == nil {
		t.Fatal("accepted missing stage")
	}
}
