//go:build darwin || linux

package projectsnapshot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/umayr/dill/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedManifestBindsSiblingImageLock(t *testing.T) {
	c, root, _, _, _ := catalogFixture(t)
	ctx := context.Background()
	digest := "docker.io/library/alpine@sha256:" + strings.Repeat("a", 64)
	lock := fmt.Sprintf(`{"version":1,"services":{"web":{"image":"alpine:latest","digest":%q}},"jobs":{}}`, digest)
	s := Snapshot{"src/main.pkl", []Entry{entry("src/dill.lock", lock), entry("src/main.pkl", "synthetic")}}
	p, err := c.Receive(ctx, root, s, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(filepath.Base(p), "snapshot-")
	if err := c.RegisterReceived(ctx, id, s); err != nil {
		t.Fatal(err)
	}
	m, err := CanonicalManifest(ctx, s, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	commitment := fmt.Sprintf("%x", sha256.Sum256(m))
	err = c.WithVerifiedEntrypoint(ctx, id, commitment, func(path string) error {
		l, err := config.ReadImageLock(filepath.Join(filepath.Dir(path), "dill.lock"))
		if err != nil {
			return err
		}
		if l == nil {
			return fmt.Errorf("missing reviewed lock")
		}
		refs, _, err := l.Resolve(map[string]string{"web": "alpine:latest"}, nil)
		if err == nil && refs["web"] != digest {
			return fmt.Errorf("lost reviewed digest")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "src", "dill.lock"), []byte(strings.Replace(lock, strings.Repeat("a", 64), strings.Repeat("b", 64), 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.WithVerifiedEntrypoint(ctx, id, commitment, func(string) error { t.Fatal("altered lock reached execution callback"); return nil }); err == nil {
		t.Fatal("accepted unreviewed image lock")
	}
}
