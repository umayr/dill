//go:build linux || darwin

package coordination

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSharedResourceCrashBlocksOtherStacks(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	a := begin(t, s, "stackA", "brine", []ResourceInput{{"container", "shared-name", "reconcile"}, {"image", "registry.invalid/test:v1", "pull"}})
	id := a.OperationID()
	a.Close()
	ctx, cancel := bounded()
	defer cancel()
	for _, r := range []ResourceInput{{"container", "shared-name", "remove"}, {"image", "registry.invalid/test:v1", "pull"}} {
		_, err := s.Begin(ctx, testTarget(), "stackB", "cli", []ResourceInput{r})
		var unresolved *InspectionRequired
		if !errors.As(err, &unresolved) || unresolved.Record.OperationID != id {
			t.Fatalf("cross-stack crash ignored: %v", err)
		}
	}
	recorded, err := s.InspectResource(ctx, testTarget().Identity(), "container", "shared-name")
	if err != nil || recorded.OperationID != id {
		t.Fatalf("resource inspection: %+v %v", recorded, err)
	}
	if err = s.AcknowledgeResource(ctx, testTarget().Identity(), "container", "shared-name", id); err != nil {
		t.Fatal(err)
	}
	// One acknowledgement coherently resolves the original stack and every one
	// of its resources; it never fabricates a no-effects outcome.
	if r := inspection(t, s, "stackA"); r.State != "acknowledged" || r.Effects != "unknown" {
		t.Fatalf("incoherent stack acknowledgement: %+v", r)
	}
	b := begin(t, s, "stackB", "cli", []ResourceInput{{"image", "registry.invalid/test:v1", "pull"}, {"container", "shared-name", "remove"}})
	b.Finish("succeeded", "complete")
	b.Close()
	// An old acknowledgement must not release/clear the newer resource owner.
	if err = s.AcknowledgeResource(ctx, testTarget().Identity(), "container", "shared-name", id); err == nil {
		t.Fatal("old ID acknowledged new resource operation")
	}
}
func TestAcknowledgeUsesSameLocksAndCannotClearActiveOperation(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	resources := []ResourceInput{{"volume", "shared-volume", "create"}, {"container", "shared-container", "reconcile"}}
	active := begin(t, s, "stackA", "brine", resources)
	for _, which := range []string{"stack", "resource"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
		var err error
		if which == "stack" {
			err = s.Acknowledge(ctx, testTarget().Identity(), "stackA", active.OperationID())
		} else {
			err = s.AcknowledgeResource(ctx, testTarget().Identity(), "volume", "shared-volume", active.OperationID())
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("acknowledged active %s: %v", which, err)
		}
	}
	active.Close()
	ctx, cancel := bounded()
	defer cancel()
	// Lock the *other* resource in the recorded inventory. AcknowledgeResource
	// must acquire this exact lock too, not re-HMAC the ID with the wrong domain.
	r := Resource{Kind: "container", ID: s.token("resource:container", "shared-container")}
	key := s.resourceKey(testTarget().Identity(), r)
	locks, err := s.acquire(ctx, [][32]byte{key})
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(context.Background(), 60*time.Millisecond)
	err = s.AcknowledgeResource(short, testTarget().Identity(), "volume", "shared-volume", active.OperationID())
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ack bypassed other inventory locks: %v", err)
	}
	release(locks)
	if err = s.AcknowledgeResource(ctx, testTarget().Identity(), "volume", "shared-volume", active.OperationID()); err != nil {
		t.Fatal(err)
	}
}
func TestPartialReservationInstallConservativelyBlocks(t *testing.T) {
	for failAt := int32(1); failAt <= 3; failAt++ {
		dir := t.TempDir()
		os.Chmod(dir, 0700)
		s := openState(t, dir)
		var commits atomic.Int32
		s.fault = func(stage string) error {
			if stage == "committed" && commits.Add(1) == failAt {
				return errors.New("persistent sync failure")
			}
			return nil
		}
		ctx, cancel := bounded()
		_, err := s.Begin(ctx, testTarget(), "A", "brine", []ResourceInput{{"container", "one", "create"}, {"volume", "two", "create"}})
		cancel()
		s.fault = nil
		if err == nil {
			t.Fatal("returned mutation permit on interrupted install")
		}
		reopened := openState(t, dir)
		r := inspection(t, reopened, "A")
		if r == nil || !needsInspection(*r) {
			t.Fatalf("lost unresolved stack: %+v", r)
		}
		ctx, cancel = bounded()
		if _, err = reopened.Begin(ctx, testTarget(), "A", "cli", nil); err == nil {
			t.Fatal("same stack bypassed partial installation")
		}
		if err = reopened.Acknowledge(ctx, testTarget().Identity(), "A", r.OperationID); err != nil {
			t.Fatal(err)
		}
		cancel()
		// Every installed reservation resolves; heads not yet installed had no
		// effects and may be used. No head belonging to another op is overwritten.
		b := begin(t, reopened, "B", "cli", []ResourceInput{{"container", "one", "create"}, {"volume", "two", "create"}})
		b.Finish("succeeded", "complete")
		b.Close()
	}
}
func TestCommitMarkerLossAndUnsafeInodePairs(t *testing.T) {
	for _, mode := range []string{"lost", "copied", "symlink", "third-link", "wrong-inode"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			s := openState(t, dir)
			operation := begin(t, s, "A", "cli", nil)
			id := operation.OperationID()
			if err := operation.Finish("succeeded", "complete"); err != nil {
				t.Fatal(err)
			}
			operation.Close()
			marker := filepath.Join(dir, commitName(id, false))
			result := filepath.Join(dir, resultName(id, false))
			switch mode {
			case "lost":
				os.Remove(marker)
			case "copied":
				data, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				os.Remove(marker)
				os.WriteFile(marker, data, 0600)
			case "symlink":
				os.Remove(marker)
				os.Symlink(result, marker)
			case "third-link":
				os.Link(result, filepath.Join(dir, "unexpected-hardlink"))
			case "wrong-inode":
				os.Remove(marker)
				os.WriteFile(marker, []byte("wrong"), 0600)
				os.Link(marker, filepath.Join(dir, "extra"))
			}
			reopened := openState(t, dir)
			ctx, cancel := bounded()
			defer cancel()
			_, err := reopened.Begin(ctx, testTarget(), "A", "brine", nil)
			var unresolved *InspectionRequired
			if !errors.As(err, &unresolved) {
				t.Fatalf("accepted missing/unsafe completion: %v", err)
			}
		})
	}
}
func TestNoGenericHardlinkExemption(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	s := openState(t, dir)
	if err := os.Link(filepath.Join(dir, "identity.key"), filepath.Join(dir, "key-link")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := bounded()
	defer cancel()
	if other, err := Open(ctx, dir); err == nil {
		other.Close()
		t.Fatal("accepted hardlinked key")
	}
	os.Remove(filepath.Join(dir, "key-link"))
	key, err := s.scope(testTarget().Identity(), "A")
	if err != nil {
		t.Fatal(err)
	}
	lockName := filepath.Join(dir, hexName(key)+".lock")
	if err = os.WriteFile(lockName, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(lockName, filepath.Join(dir, "lock-link")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Begin(ctx, testTarget(), "A", "cli", nil); err == nil {
		t.Fatal("accepted hardlinked lock")
	}
	// File shape checking remains nonblocking on FIFOs.
	os.Remove(lockName)
	os.Remove(filepath.Join(dir, "lock-link"))
	if err = unix.Mkfifo(lockName, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Begin(ctx, testTarget(), "A", "cli", nil); err == nil {
		t.Fatal("accepted FIFO lock")
	}
}
func hexName(key [32]byte) string { return nameFor(key)[:64] }
