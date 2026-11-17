package hostagent

import (
	"context"
	"crypto/ed25519"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUnixPipelinedNonreaderDeadlineAndRecovery(t *testing.T) {
	dir, e := os.MkdirTemp("/tmp", "agent-write-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "agent.sock")
	listener, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id")})
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	conn, e := net.Dial("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if e = conn.(*net.UnixConn).SetReadBuffer(1024); e != nil {
		t.Fatal(e)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(14 * time.Second))
	body := `{"nonce":"` + raw.EncodeToString(make([]byte, 32)) + `"}`
	request := "POST /v1/session HTTP/1.1\r\nHost: agent\r\nContent-Type: application/json\r\nX-Request-Id: " + strings.Repeat("a", 32) + "\r\nX-Brine-Binding: 1\r\nContent-Length: " + "55" + "\r\n\r\n" + body
	// Derive length explicitly so changes to the nonce encoding do not alter framing.
	request = strings.Replace(request, "Content-Length: 55", "Content-Length: "+strconv.Itoa(len(body)), 1)
	done := make(chan error, 1)
	go func() { _, err := io.WriteString(conn, strings.Repeat(request, 30000)); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("non-reading pipelined client did not block")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("non-reading client was not disconnected by write deadline")
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	req, _ := http.NewRequest("POST", "http://agent/v1/session", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", strings.Repeat("b", 32))
	req.Header.Set("X-Brine-Binding", "1")
	resp, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}
