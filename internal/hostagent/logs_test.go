package hostagent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingLogs struct{ opened chan *blockingReader }
type blockingReader struct {
	done  chan struct{}
	once  sync.Once
	first bool
}

func (r *blockingReader) Next() (string, []byte, error) {
	if !r.first {
		r.first = true
		return "stdout", []byte("first"), nil
	}
	<-r.done
	return "", nil, io.EOF
}
func (r *blockingReader) Close() error { r.once.Do(func() { close(r.done) }); return nil }
func (b *blockingLogs) OpenLogs(context.Context, string, int, bool, bool) (LogReader, error) {
	r := &blockingReader{done: make(chan struct{})}
	b.opened <- r
	return r, nil
}
func TestLogsCancellationAndConcurrency(t *testing.T) {
	cid := strings.Repeat("a", 64)
	labels := map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}
	inventory := &inventoryMock{inspections: map[string]InventoryContainer{cid: {ID: cid, Labels: labels}}}
	backend := &blockingLogs{opened: make(chan *blockingReader, 3)}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InventoryBackend: inventory, LogsBackend: backend})
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	open := func(ctx context.Context) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/logs", strings.NewReader(`{"containerId":"`+cid+`","tail":1,"follow":true,"timestamps":false}`))
		req.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		req.Header.Set("X-Brine-Binding", "1")
		req.Header.Set("X-Agent-Epoch", s.epoch)
		req.Header.Set("Content-Type", "application/json")
		return srv.Client().Do(req)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r1, e := open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	reader1 := <-backend.opened
	line, e := bufio.NewReader(r1.Body).ReadBytes('\n')
	if e != nil {
		t.Fatal(e)
	}
	var frame map[string]any
	if json.Unmarshal(line, &frame) != nil || !strings.Contains(string(line), `"bytesBase64":"Zmlyc3Q="`) {
		t.Fatal(string(line))
	}
	r2, e := open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	reader2 := <-backend.opened
	r3, e := open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if r3.StatusCode != 413 {
		t.Fatal(r3.StatusCode)
	}
	r3.Body.Close()
	cancel()
	r1.Body.Close()
	select {
	case <-reader1.done:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not close engine stream")
	}
	r2.Body.Close()
	select {
	case <-reader2.done:
	case <-time.After(3 * time.Second):
		t.Fatal("second stream not cancelled")
	}
}
