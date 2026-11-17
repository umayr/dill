package hostagent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdleFollowSurvivesWriteDeadlineUntilHeartbeat(t *testing.T) {
	cid := strings.Repeat("a", 64)
	labels := map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}
	inventory := &inventoryMock{inspections: map[string]InventoryContainer{cid: {ID: cid, Labels: labels}}}
	backend := &blockingLogs{opened: make(chan *blockingReader, 1)}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InventoryBackend: inventory, LogsBackend: backend})
	if e != nil {
		t.Fatal(e)
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 36*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/v1/logs", strings.NewReader(`{"containerId":"`+cid+`","tail":1,"follow":true,"timestamps":false}`))
	req.Header.Set("X-Request-Id", strings.Repeat("a", 32))
	req.Header.Set("X-Brine-Binding", "1")
	req.Header.Set("X-Agent-Epoch", s.epoch)
	req.Header.Set("Content-Type", "application/json")
	resp, e := srv.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if _, e = r.ReadString('\n'); e != nil {
		t.Fatal(e)
	}
	line, e := r.ReadString('\n')
	if e != nil || !strings.Contains(line, `"type":"heartbeat"`) {
		t.Fatalf("heartbeat %q %v", line, e)
	}
}
