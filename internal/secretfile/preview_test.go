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
