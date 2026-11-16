package projectsnapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
)

func TestCanonicalManifest(t *testing.T) {
	a, b := []byte("a"), []byte("bb")
	s := Snapshot{Entrypoint: "z.pkl", Files: []Entry{{Path: "z.pkl", Content: b, SHA256: sha256.Sum256(b)}, {Path: "a", Content: a, SHA256: sha256.Sum256(a)}}}
	first, err := CanonicalManifest(context.Background(), s, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	s.Files[0], s.Files[1] = s.Files[1], s.Files[0]
	second, err := CanonicalManifest(context.Background(), s, MaximumLimits())
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("noncanonical: %s %s %v", first, second, err)
	}
	if !strings.Contains(string(first), `"entrypoint":"z.pkl"`) || !strings.Contains(string(first), `"path":"z.pkl","length":2`) {
		t.Fatalf("incorrect manifest: %s", first)
	}
	s.Entrypoint = "Z.pkl"
	if _, err := CanonicalManifest(context.Background(), s, MaximumLimits()); err == nil {
		t.Fatal("accepted non-exact entrypoint")
	}
	s.Entrypoint = "z.pkl"
	s.Files[0].Content[0] = 'x'
	if _, err := CanonicalManifest(context.Background(), s, MaximumLimits()); err == nil {
		t.Fatal("accepted incorrect hash")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CanonicalManifest(ctx, s, MaximumLimits()); err == nil {
		t.Fatal("accepted cancellation")
	}
}
