package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/hostagent"
)

func TestDockerLogsUnsupportedDriverAndFrames(t *testing.T) {
	id := strings.Repeat("a", 64)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			io.WriteString(w, `{"Id":"`+id+`","Config":{"Labels":{"dill.managed":"true","dill.stack":"s","dill.service":"v"}},"HostConfig":{"LogConfig":{"Type":"syslog"}}}`)
			return
		}
		requests++
		w.WriteHeader(500)
	}))
	defer server.Close()
	// Rewrite host only in this isolated adapter unit test.
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	d := dockerIdentity{client: &http.Client{Transport: tr}}
	_, e := d.OpenLogs(context.Background(), id, 1, false, false)
	if !errors.Is(e, hostagent.ErrUnsupported) || requests != 0 {
		t.Fatalf("unsupported %v requests %d", e, requests)
	}
	header := make([]byte, 8)
	header[0] = 2
	binary.BigEndian.PutUint32(header[4:], 20000)
	reader := &dockerLogReader{body: io.NopCloser(bytes.NewReader(append(header, bytes.Repeat([]byte{'x'}, 20000)...)))}
	stream, b, e := reader.Next()
	if e != nil || stream != "stderr" || len(b) != 16384 {
		t.Fatal(stream, len(b), e)
	}
	_, b, e = reader.Next()
	if e != nil || len(b) != 3616 {
		t.Fatal(len(b), e)
	}
	_, _, e = reader.Next()
	if e != io.EOF {
		t.Fatal(e)
	}
	for _, input := range [][]byte{{1, 0, 0}, {1, 0, 0, 0, 0, 0, 0, 3}, {1, 0, 0, 0, 0, 0, 0, 3, 'a'}} {
		r := &dockerLogReader{body: io.NopCloser(bytes.NewReader(input))}
		_, _, err := r.Next()
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("truncated frame: %v", err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
