package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/hostagent"
)

func TestDockerIdentityAndRevealUnix(t *testing.T) {
	dir, e := os.MkdirTemp("/tmp", "da-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "docker.sock")
	l, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	id := strings.Repeat("a", 64)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_ = json.NewEncoder(w).Encode(map[string]string{"ID": "daemon"})
		case "/containers/json":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"Id": id, "Labels": map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}}})
		case "/containers/" + id + "/json":
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Config": map[string]any{"Labels": map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}, "Env": []string{"A=value"}}, "HostConfig": map[string]any{"LogConfig": map[string]string{"Type": "json-file"}}})
		case "/containers/" + id + "/logs":
			if r.URL.Query().Get("tail") != "3" {
				t.Errorf("unexpected tail %s", r.URL.RawQuery)
			}
			header := make([]byte, 8)
			header[0] = 1
			binary.BigEndian.PutUint32(header[4:], 3)
			_, _ = w.Write(append(header, []byte("abc")...))
		default:
			http.NotFound(w, r)
		}
	})
	server := &http.Server{Handler: h}
	go server.Serve(l)
	defer server.Close()
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer tr.CloseIdleConnections()
	d := dockerIdentity{client: &http.Client{Transport: tr}}
	got, e := d.DaemonID(context.Background())
	if e != nil || got != "daemon" {
		t.Fatalf("identity: %q %v", got, e)
	}
	c, e := d.RevealInspect(context.Background(), id)
	if e != nil || c.ID != id || len(c.Env) != 1 {
		t.Fatalf("inspect: %+v %v", c, e)
	}
	agent, e := hostagent.New(hostagent.Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix://" + path, DaemonID: "daemon", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: d, InventoryBackend: d, InspectBackend: d, LogsBackend: d})
	if e != nil {
		t.Fatal(e)
	}
	agentPath := filepath.Join(dir, "agent.sock")
	agentListener, e := net.Listen("unix", agentPath)
	if e != nil {
		t.Fatal(e)
	}
	agentServer := &http.Server{Handler: agent}
	go agentServer.Serve(agentListener)
	defer agentServer.Close()
	agentTransport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", agentPath)
	}}
	defer agentTransport.CloseIdleConnections()
	ac := &http.Client{Transport: agentTransport}
	post := func(route, body, epoch string) []byte {
		req, e := http.NewRequest("POST", "http://agent"+route, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Brine-Binding", "1")
		req.Header.Set("X-Request-Id", strings.Repeat("1", 32))
		if epoch != "" {
			req.Header.Set("X-Agent-Epoch", epoch)
		}
		resp, e := ac.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil || resp.StatusCode != 200 {
			t.Fatalf("agent %d %s %v", resp.StatusCode, b, e)
		}
		return b
	}
	nonce := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	var session struct{ Data struct{ Epoch string } }
	if e = json.Unmarshal(post("/v1/session", `{"nonce":"`+nonce+`"}`, ""), &session); e != nil {
		t.Fatal(e)
	}
	response := post("/v1/inventory", `{"cursor":null,"limit":10}`, session.Data.Epoch)
	if !strings.Contains(string(response), id) || strings.Contains(string(response), "A=value") {
		t.Fatalf("inventory response %s", response)
	}
	inspected := post("/v1/inspect", `{"containerId":"`+id+`"}`, session.Data.Epoch)
	if strings.Contains(string(inspected), "A=value") || !strings.Contains(string(inspected), `"envKeys":["A"]`) {
		t.Fatalf("inspect response %s", inspected)
	}
	logs := post("/v1/logs", `{"containerId":"`+id+`","tail":3,"follow":false,"timestamps":false}`, session.Data.Epoch)
	if !strings.Contains(string(logs), `"bytesBase64":"YWJj"`) || !strings.Contains(string(logs), `"reason":"complete"`) {
		t.Fatalf("logs %s", logs)
	}
	_, e = d.RevealInspect(context.Background(), strings.Repeat("b", 64))
	if e != hostagent.ErrNotFound {
		t.Fatalf("not found: %v", e)
	}
}
