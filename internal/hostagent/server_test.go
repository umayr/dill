package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type identity string

func (i identity) DaemonID(context.Context) (string, error) { return string(i), nil }
func TestSessionAndFailClosed(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	c := Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///tmp/docker.sock", DaemonID: "daemon", PrivateKey: key, Identity: identity("daemon")}
	s, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	call := func(path, body, epoch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("Content-Type", "application/json")
		if epoch != "" {
			r.Header.Set("X-Agent-Epoch", epoch)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	nonce := raw.EncodeToString(make([]byte, 32))
	w := call("/v1/session", `{"nonce":"`+nonce+`"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Data struct{ Epoch, Signature string }
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	msg, _ := SessionBytes(nonce, s.agentID, s.public, c.EngineID, 1, c.EngineKind, c.EngineSocket, c.DaemonID, result.Data.Epoch)
	sig, _ := raw.DecodeString(result.Data.Signature)
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), msg, sig) {
		t.Fatal("signature")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("cache")
	}
	if call("/v1/session", `{"nonce":"`+nonce+`","nonce":"`+nonce+`"}`, "").Code != 400 {
		t.Fatal("duplicate accepted")
	}
	if call("/v1/session", `{"nonce":"`+nonce+`","extra":1}`, "").Code != 400 {
		t.Fatal("unknown accepted")
	}
	if call("/v1/reveal", `{}`, result.Data.Epoch).Code != 501 {
		t.Fatal("reveal enabled")
	}
	if call("/v1/reveal", `{}`, "stale").Code != 403 {
		t.Fatal("stale epoch")
	}
	if call("/legacy/mutate", `{}`, "").Code != 400 {
		t.Fatal("legacy enabled")
	}
	s2, _ := New(c)
	if s2.epoch == s.epoch {
		t.Fatal("epoch reused")
	}
	s.c.Identity = identity("other")
	if call("/v1/session", `{"nonce":"`+nonce+`"}`, "").Code != 409 {
		t.Fatal("identity mismatch")
	}
}
func TestSessionEncoding(t *testing.T) {
	b, e := SessionBytes("n", "a", "p", "e", 1, "k", "s", "d", "x")
	if e != nil {
		t.Fatal(e)
	}
	want := append([]byte("brine-agent-session/1\x00"), []byte{0, 0, 0, 1, 'n', 0, 0, 0, 1, 'a', 0, 0, 0, 1, 'p', 0, 0, 0, 1, 'e', 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 1, 'k', 0, 0, 0, 1, 's', 0, 0, 0, 1, 'd', 0, 0, 0, 1, 'x'}...)
	if !bytes.Equal(b, want) {
		t.Fatal("canonical bytes differ")
	}
	if _, e = SessionBytes(strings.Repeat("x", 4097), "", "", "", 1, "", "", "", ""); e == nil {
		t.Fatal("oversize accepted")
	}
}
