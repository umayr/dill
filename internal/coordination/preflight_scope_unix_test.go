//go:build linux || darwin

package coordination

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestNamedPreflightScopeAliasesAndOwnMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.NamedPreflightScope()
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	viaAlias, err := other.NamedPreflightScope()
	if err != nil || viaAlias != first {
		t.Fatal("directory alias changed identity", err)
	}
	// The invocation's own metadata must not invalidate its comparison.
	if err = os.WriteFile(filepath.Join(root, "synthetic-metadata"), []byte("not a reservation"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := s.NamedPreflightScope()
	if err != nil || after != first {
		t.Fatal("mutable directory contents entered identity", err)
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(s.dir.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 24)
	binary.BigEndian.PutUint64(payload, uint64(st.Dev))
	binary.BigEndian.PutUint64(payload[8:], uint64(st.Ino))
	binary.BigEndian.PutUint64(payload[16:], uint64(os.Geteuid()))
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte("dill.named-store/1\x00"))
	mac.Write(payload)
	if first != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("frozen HMAC domain/byte order differs")
	}
}
func TestNamedPreflightScopeReplacementCopiedKeyAndClosedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.NamedPreflightScope()
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "retained")
	if err = os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Even a copied private key is not the same retained physical directory.
	if err = os.WriteFile(filepath.Join(root, "identity.key"), s.key, 0600); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := other.NamedPreflightScope()
	if err != nil || replacement == first {
		t.Fatal("replacement/copied store identity reused", err)
	}
	pinned, err := s.NamedPreflightScope()
	if err != nil || pinned != first {
		t.Fatal("retained directory followed replacement path", err)
	}
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = other.NamedPreflightScope(); err == nil {
		t.Fatal("closed state identity was usable")
	}
	if _, err = (*State)(nil).NamedPreflightScope(); err == nil {
		t.Fatal("nil state identity was usable")
	}
}
