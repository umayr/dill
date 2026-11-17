package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingIdentity struct{ count atomic.Int32 }

func (i *countingIdentity) DaemonID(context.Context) (string, error) {
	i.count.Add(1)
	return "id", nil
}

func TestRevealBytesV2Fixture(t *testing.T) {
	g := Grant{AgentID: "agent", EngineID: "engine", Generation: 7, Epoch: "epoch", ContainerID: "container", SessionID: "session", RequestID: "0123456789abcdef0123456789abcdef", Nonce: "nonce", IssuedAt: 10, ExpiresAt: 20, Scope: "env.values", Interactive: true}
	b, err := RevealBytes(g)
	const want = "6272696e652d6167656e742d72657665616c2f3200000000056167656e7400000006656e67696e6500000000000000070000000565706f636800000009636f6e7461696e65720000000773657373696f6e000000056e6f6e6365000000203031323334353637383961626364656630313233343536373839616263646566000000000000000a00000000000000140000000a656e762e76616c75657301"
	if err != nil || hex.EncodeToString(b) != want {
		t.Fatalf("signature input %x: %v", b, err)
	}
}

func TestRevealRequestIDBinding(t *testing.T) {
	signer := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))
	identity := &countingIdentity{}
	backend := &revealMock{labels: map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "v"}}
	s, err := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, 32)), Identity: identity, RevealKey: signer.Public().(ed25519.PublicKey), RevealBackend: backend})
	if err != nil {
		t.Fatal(err)
	}
	cid, id, other := strings.Repeat("a", 64), strings.Repeat("b", 32), strings.Repeat("c", 32)
	now := time.Now().Unix()
	g := Grant{AgentID: s.agentID, EngineID: s.c.EngineID, Generation: 1, Epoch: s.epoch, ContainerID: cid, SessionID: raw.EncodeToString(make([]byte, 16)), RequestID: id, Nonce: raw.EncodeToString(bytes.Repeat([]byte{9}, 32)), IssuedAt: now, ExpiresAt: now + 20, Scope: "env.values", Interactive: true}
	sign := func(g *Grant) {
		b, e := RevealBytes(*g)
		if e != nil {
			t.Fatal(e)
		}
		g.Signature = raw.EncodeToString(ed25519.Sign(signer, b))
	}
	sign(&g)
	call := func(grant any, headers ...string) int {
		body, _ := json.Marshal(map[string]any{"containerId": cid, "grant": grant})
		r := httptest.NewRequest("POST", "/v1/reveal", bytes.NewReader(body))
		for _, h := range headers {
			r.Header.Add("X-Request-Id", h)
		}
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("X-Agent-Epoch", s.epoch)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	malformed := g
	malformed.RequestID = strings.ToUpper(id)
	sign(&malformed)
	forged := g
	forged.RequestID = other
	legacy := g
	// Construct the original v1 input independently: it has no request ID.
	var legacyBytes bytes.Buffer
	legacyBytes.WriteString("brine-agent-reveal/1\x00")
	for _, v := range []string{g.AgentID, g.EngineID} {
		_ = lp(&legacyBytes, v)
	}
	_ = binary.Write(&legacyBytes, binary.BigEndian, g.Generation)
	for _, v := range []string{g.Epoch, g.ContainerID, g.SessionID, g.Nonce} {
		_ = lp(&legacyBytes, v)
	}
	_ = binary.Write(&legacyBytes, binary.BigEndian, uint64(g.IssuedAt))
	_ = binary.Write(&legacyBytes, binary.BigEndian, uint64(g.ExpiresAt))
	_ = lp(&legacyBytes, g.Scope)
	legacyBytes.WriteByte(1)
	legacy.Signature = raw.EncodeToString(ed25519.Sign(signer, legacyBytes.Bytes()))
	missing := map[string]any{}
	b, _ := json.Marshal(g)
	_ = json.Unmarshal(b, &missing)
	delete(missing, "requestId")
	for _, tc := range []struct {
		name    string
		grant   any
		headers []string
		status  int
	}{
		{"mismatch", g, []string{other}, 403}, {"forged", forged, []string{other}, 403}, {"legacy domain", legacy, []string{id}, 403}, {"malformed signed", malformed, []string{strings.ToUpper(id)}, 400}, {"malformed grant", malformed, []string{id}, 403}, {"missing grant", missing, []string{id}, 400}, {"missing header", g, nil, 400}, {"duplicate header", g, []string{id, id}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := call(tc.grant, tc.headers...); got != tc.status {
				t.Fatalf("status %d want %d", got, tc.status)
			}
			if identity.count.Load() != 0 || backend.count.Load() != 0 {
				t.Fatal("invalid request accessed engine")
			}
		})
	}
	grantJSON, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, extra string }{
		{"duplicate", `,"requestId":"` + id + `"`},
		{"escaped equivalent", `,"request\u0049d":"` + id + `"`},
		{"unknown", `,"unexpected":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := append(append([]byte{}, grantJSON[:len(grantJSON)-1]...), []byte(tc.extra+"}")...)
			if got := call(json.RawMessage(altered), id); got != 400 {
				t.Fatalf("status %d", got)
			}
			if identity.count.Load() != 0 || backend.count.Load() != 0 {
				t.Fatal("invalid JSON accessed engine")
			}
		})
	}
	if got := call(g, id); got != 200 {
		t.Fatalf("valid grant after rejected attempts: %d", got)
	}
	second := g
	second.RequestID = other
	sign(&second)
	if got := call(second, other); got != 403 {
		t.Fatalf("same nonce with independently signed request ID: %d", got)
	}
	if got := call(g, id); got != 403 {
		t.Fatalf("replay: %d", got)
	}
	if got := call(g, other); got != 403 {
		t.Fatalf("replay with different id: %d", got)
	}
	if identity.count.Load() != 1 || backend.count.Load() != 1 {
		t.Fatal("replay accessed engine")
	}
}

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
		g := Grant{AgentID: s.agentID, EngineID: s.c.EngineID, Generation: 1, Epoch: s.epoch, ContainerID: cid, SessionID: raw.EncodeToString(make([]byte, 16)), RequestID: strings.Repeat("b", 32), Nonce: raw.EncodeToString(bytes.Repeat([]byte{n}, 32)), IssuedAt: now, ExpiresAt: now + 20, Scope: "env.values", Interactive: true}
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
