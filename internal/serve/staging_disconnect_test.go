package serve

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type disconnectedWriter struct {
	header http.Header
	code   int
}

func (w *disconnectedWriter) Header() http.Header     { return w.header }
func (w *disconnectedWriter) WriteHeader(code int)    { w.code = code }
func (*disconnectedWriter) Write([]byte) (int, error) { return 0, errors.New("disconnected") }
func TestDisconnectAfterPublicationRetainsSnapshot(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := &disconnectedWriter{header: make(http.Header)}
	API{Stager: s}.ServeHTTP(w, r)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "snapshot-") {
		t.Fatalf("published stage missing: %v %v", entries, err)
	}
}
