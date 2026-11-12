package secretfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestPreviewMACFraming(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	a := previewMAC(key, []byte("ab"), []byte("c"))
	b := previewMAC(key, []byte("a"), []byte("bc"))
	if a == b || a != previewMAC(key, []byte("ab"), []byte("c")) {
		t.Fatal("MAC is unstable or framing is ambiguous")
	}
	if a == previewMAC(bytes.Repeat([]byte{0x43}, 32), []byte("ab"), []byte("c")) {
		t.Fatal("key rotation did not invalidate")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(a); err != nil || len(raw) != 32 {
		t.Fatal("invalid opaque proof encoding")
	}
	// Independent Python hmac/hashlib fixture, domain and uint64-be lengths.
	if a != "bUwO8p3U5LJXWdKhGLtcrTybY3biloawADffsETk5hM" {
		t.Fatalf("MAC framing vector mismatch: %s", a)
	}
}

// Public synthetic wire fixture for paired consumers. This checks encoding and
// the five-frame protocol, not availability or actual runtime authorization.
func TestPreviewWireGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/preview-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if json.Unmarshal(b, &doc) != nil || len(doc) != 2 || doc["contract"] != PreviewContract {
		t.Fatal("invalid golden wire schema")
	}
	proof := previewMAC(bytes.Repeat([]byte{0x42}, 32), []byte("{}"), []byte("{}"), []byte("{}"), []byte(`{"changes":[]}`))
	if proof != doc["proof"] {
		t.Fatal("independent five-frame golden mismatch")
	}
}

// Independent Python hmac/hashlib vector covering private, observed stat facts.
func TestPreviewObservedSourcesMACVector(t *testing.T) {
	base := sourceObservation{Device: 7, Inode: 9, Size: 11, MTime: sourceTimestamp{101, 202}, CTime: sourceTimestamp{303, 404}, UID: 1000, GID: 1001, Mode: 33024, Links: 1}
	mac := func(o sourceObservation) string {
		t.Helper()
		authority, err := CanonicalPreviewJSON(struct {
			Sources map[string][]sourceObservation
		}{map[string][]sourceObservation{"app": {o}}})
		if err != nil {
			t.Fatal(err)
		}
		return previewMAC(bytes.Repeat([]byte{0x42}, 32), authority, []byte("{}"), []byte("{}"), []byte(`{"changes":[]}`))
	}
	first := mac(base)
	if first != "kLo8GUAJnshkulAdoSg_24fy-FvNXLwwfBm3vyNblyk" {
		t.Fatal("observed stat independent vector mismatch")
	}
	for name, change := range map[string]func(*sourceObservation){"device": func(o *sourceObservation) { o.Device++ }, "inode": func(o *sourceObservation) { o.Inode++ }, "size": func(o *sourceObservation) { o.Size++ }, "mtime": func(o *sourceObservation) { o.MTime.Nanoseconds++ }, "ctime": func(o *sourceObservation) { o.CTime.Nanoseconds++ }, "owner": func(o *sourceObservation) { o.UID++ }, "group": func(o *sourceObservation) { o.GID++ }, "mode": func(o *sourceObservation) { o.Mode++ }, "links": func(o *sourceObservation) { o.Links++ }} {
		t.Run(name, func(t *testing.T) {
			next := base
			change(&next)
			if mac(next) == first {
				t.Fatal("observed field omitted from MAC")
			}
		})
	}
}
