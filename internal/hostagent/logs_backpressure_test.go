package hostagent

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingLogWriter struct {
	header    http.Header
	deadlines int
}

func (w *failingLogWriter) Header() http.Header              { return w.header }
func (w *failingLogWriter) WriteHeader(int)                  {}
func (w *failingLogWriter) Write([]byte) (int, error)        { return 0, errors.New("client write blocked") }
func (w *failingLogWriter) FlushError() error                { return nil }
func (w *failingLogWriter) SetWriteDeadline(time.Time) error { w.deadlines++; return nil }
func TestLogWriteFailureClosesEngine(t *testing.T) {
	cid := strings.Repeat("a", 64)
	labels := map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}
	m := &inventoryMock{inspections: map[string]InventoryContainer{cid: {ID: cid, Labels: labels}}}
	backend := &blockingLogs{opened: make(chan *blockingReader, 1)}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InventoryBackend: m, LogsBackend: backend})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", "/v1/logs", strings.NewReader(`{"containerId":"`+cid+`","tail":1,"follow":true,"timestamps":false}`))
	r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
	r.Header.Set("X-Brine-Binding", "1")
	r.Header.Set("X-Agent-Epoch", s.epoch)
	r.Header.Set("Content-Type", "application/json")
	w := &failingLogWriter{header: make(http.Header)}
	s.ServeHTTP(w, r)
	reader := <-backend.opened
	select {
	case <-reader.done:
	default:
		t.Fatal("engine stream not closed")
	}
	if w.deadlines < 2 {
		t.Fatal("per-write deadline missing")
	}
}
