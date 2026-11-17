package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type revealMock struct {
	count  atomic.Int32
	labels map[string]string
	env    []string
}

func (m *revealMock) RevealInspect(_ context.Context, id string) (RevealContainer, error) {
	m.count.Add(1)
	return RevealContainer{ID: id, Labels: m.labels, Env: m.env}, nil
}
func TestRevealSecurity(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32))
	signer := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	m := &revealMock{labels: map[string]string{"dill.managed": "true", "dill.stack": "stack", "dill.service": "service"}, env: []string{"A=secret"}}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: key, Identity: identity("id"), RevealKey: signer.Public().(ed25519.PublicKey), RevealBackend: m})
	if e != nil {
		t.Fatal(e)
	}
	cid := strings.Repeat("a", 64)
	now := time.Now().Unix()
	grant := func(n byte) Grant {
		g := Grant{AgentID: s.agentID, EngineID: s.c.EngineID, Generation: 1, Epoch: s.epoch, ContainerID: cid, SessionID: raw.EncodeToString(make([]byte, 16)), Nonce: raw.EncodeToString(bytes.Repeat([]byte{n}, 32)), IssuedAt: now, ExpiresAt: now + 20, Scope: "env.values", Interactive: true}
		b, _ := RevealBytes(g)
		g.Signature = raw.EncodeToString(ed25519.Sign(signer, b))
		return g
	}
	call := func(g Grant) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"containerId": cid, "grant": g})
		r := httptest.NewRequest("POST", "/v1/reveal", bytes.NewReader(b))
		r.Header.Set("X-Request-Id", strings.Repeat("b", 32))
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("X-Agent-Epoch", s.epoch)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	g := grant(1)
	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call(g).Code == 200 {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || m.count.Load() != 1 {
		t.Fatalf("success %d accesses %d", success.Load(), m.count.Load())
	}
	bad := grant(2)
	bad.Scope = "other"
	if call(bad).Code != 403 {
		t.Fatal("tampered grant")
	}
	if m.count.Load() != 1 {
		t.Fatal("invalid grant accessed engine")
	}
	foreign := grant(3)
	m.labels = map[string]string{"dill.managed": "true"}
	if call(foreign).Code != 404 {
		t.Fatal("foreign disclosed")
	}
	if call(foreign).Code != 403 {
		t.Fatal("failed access not consumed")
	}
	m.labels = map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "v"}
	m.env = []string{"X=1", "X=2"}
	if call(grant(4)).Code != 500 {
		t.Fatal("duplicate environment disclosed")
	}
	stale := grant(5)
	stale.ExpiresAt = now - 10
	stale.IssuedAt = now - 20
	b, _ := RevealBytes(stale)
	stale.Signature = raw.EncodeToString(ed25519.Sign(signer, b))
	if call(stale).Code != 403 {
		t.Fatal("expired grant")
	}
	s.mu.Lock()
	s.lastClock = time.Now().Add(time.Hour)
	s.mu.Unlock()
	if call(grant(6)).Code != 403 {
		t.Fatal("clock rollback")
	}
}
