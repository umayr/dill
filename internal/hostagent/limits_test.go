package hostagent

import (
	"crypto/ed25519"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pipeWriter struct {
	c net.Conn
	h http.Header
}

func (w *pipeWriter) Header() http.Header                { return w.h }
func (w *pipeWriter) WriteHeader(int)                    {}
func (w *pipeWriter) Write(b []byte) (int, error)        { return w.c.Write(b) }
func (w *pipeWriter) SetWriteDeadline(t time.Time) error { return w.c.SetWriteDeadline(t) }
func TestNonstreamBlockedReadersReleaseCapacity(t *testing.T) {
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id")})
	if e != nil {
		t.Fatal(e)
	}
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/v1/session", strings.NewReader(`{"nonce":"`+raw.EncodeToString(make([]byte, 32))+`"}`))
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	var wg sync.WaitGroup
	peers := make([]net.Conn, 0, 16)
	for i := 0; i < 16; i++ {
		a, b := net.Pipe()
		peers = append(peers, b)
		wg.Add(1)
		go func() { defer wg.Done(); defer a.Close(); s.ServeHTTP(&pipeWriter{a, make(http.Header)}, request()) }()
	}
	defer func() {
		for _, p := range peers {
			p.Close()
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(s.operations) != 16 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.operations) != 16 {
		t.Fatal("slots not acquired")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, request())
	if w.Code != 413 {
		t.Fatal(w.Code)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("blocked writes did not time out")
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, request())
	if w.Code != 200 {
		t.Fatal("capacity not recovered", w.Code)
	}
}
