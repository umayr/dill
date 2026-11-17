package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http/httptest"
	"strings"
	"testing"
)

type inspectMock struct{ result InspectResult }

func (m *inspectMock) Inspect(context.Context, string) (InspectResult, error) { return m.result, nil }
func TestInspectScopeAndBounds(t *testing.T) {
	cid := strings.Repeat("a", 64)
	m := &inspectMock{result: InspectResult{Labels: map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "v", "secret": "x"}, Profile: InspectProfile{ID: cid, Command: []string{}, EnvKeys: []string{"A"}, Labels: map[string]string{"secret": "x"}, Ports: []InspectPort{}, Mounts: []InspectMount{}, Networks: []string{}, Health: InspectHealth{Test: []string{}}, Security: InspectSecurity{CapAdd: []string{}}}}}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InspectBackend: m})
	if e != nil {
		t.Fatal(e)
	}
	call := func(container string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/inspect", bytes.NewBufferString(`{"containerId":"`+container+`"}`))
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Brine-Binding", "1")
		r.Header.Set("X-Agent-Epoch", s.epoch)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	w := call(cid)
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	if call("a").Code != 400 {
		t.Fatal("prefix accepted")
	}
	m.result.Labels["dill.managed"] = "false"
	if call(cid).Code != 404 {
		t.Fatal("foreign disclosed")
	}
	m.result.Labels["dill.managed"] = "true"
	m.result.Profile.EnvKeys = []string{"A", "A"}
	if call(cid).Code != 500 {
		t.Fatal("duplicate keys")
	}
	m.result.Profile.EnvKeys = []string{"A"}
	m.result.Profile.Command = []string{strings.Repeat("x", 8193)}
	if call(cid).Code != 413 {
		t.Fatal("oversize command")
	}
}
