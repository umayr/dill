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
	"testing"
)

type floodBackend struct{}
type floodReader struct{}

func (floodBackend) OpenLogs(context.Context, string, int, bool, bool) (LogReader, error) {
	return floodReader{}, nil
}
func (floodReader) Next() (string, []byte, error) { return "stdout", make([]byte, 16384), nil }
func (floodReader) Close() error                  { return nil }
func TestLogsByteLimit(t *testing.T) {
	cid := strings.Repeat("a", 64)
	labels := map[string]string{"dill.managed": "true", "dill.stack": "s", "dill.service": "x"}
	m := &inventoryMock{inspections: map[string]InventoryContainer{cid: {ID: cid, Labels: labels}}}
	s, e := New(Config{EngineID: "11111111-1111-1111-1111-111111111111", Generation: 1, EngineKind: "docker", EngineSocket: "unix:///engine", DaemonID: "id", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: identity("id"), InventoryBackend: m, LogsBackend: floodBackend{}})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/logs", strings.NewReader(`{"containerId":"`+cid+`","tail":1,"follow":false,"timestamps":false}`))
	req.Header.Set("X-Request-Id", strings.Repeat("a", 32))
	req.Header.Set("X-Brine-Binding", "1")
	req.Header.Set("X-Agent-Epoch", s.epoch)
	req.Header.Set("Content-Type", "application/json")
	resp, e := server.Client().Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	count := 0
	ended := false
	for scan.Scan() {
		var frame struct {
			Data struct {
				Type     string `json:"type"`
				Reason   string `json:"reason"`
				Bytes    int    `json:"bytes"`
				Sequence int    `json:"sequence"`
			}
		}
		if json.Unmarshal(scan.Bytes(), &frame) != nil {
			t.Fatal("invalid frame")
		}
		if frame.Data.Type == "chunk" {
			count++
			if frame.Data.Sequence != count {
				t.Fatal("sequence")
			}
		}
		if frame.Data.Type == "end" {
			ended = frame.Data.Reason == "byte_limit" && frame.Data.Bytes == 1<<20
		}
	}
	if scan.Err() != nil && scan.Err() != io.EOF {
		t.Fatal(scan.Err())
	}
	if count != 64 || !ended {
		t.Fatalf("chunks %d end %v", count, ended)
	}
}
