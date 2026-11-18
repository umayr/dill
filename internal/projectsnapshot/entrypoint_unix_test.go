//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithVerifiedEntrypoint(t *testing.T) {
	c, stages, _, snapshot, id := catalogFixture(t)
	ctx := context.Background()
	manifest, err := CanonicalManifest(ctx, snapshot, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(manifest))
	never := func(string) error { t.Error("called consumer for an unverified stage"); return nil }
	if err := c.WithVerifiedEntrypoint(ctx, id, digest, never); err == nil {
		t.Fatal("allowed unpublished catalog record")
	}
	if err := c.RegisterReceived(ctx, id, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := c.WithVerifiedEntrypoint(ctx, id, strings.Repeat("0", 64), never); err == nil {
		t.Fatal("accepted a different reviewed digest")
	}
	entry := filepath.Join(stages, "snapshot-"+id, "a", "main.pkl")
	called := false
	err = c.WithVerifiedEntrypoint(ctx, id, digest, func(path string) error {
		called = true
		if path != entry {
			t.Fatal("consumer did not get the catalog entrypoint")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("consumer not called: %v", err)
	}
	if err := os.WriteFile(entry, []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.WithVerifiedEntrypoint(ctx, id, digest, never); err == nil {
		t.Fatal("accepted modified staged project")
	}
}
