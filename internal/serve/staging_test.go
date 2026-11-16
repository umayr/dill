package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpload(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("example\x00bytes")
	h := sha256.Sum256(data)
	body := fmt.Sprintf(`{"schema":"dill.snapshot.v1","entrypoint":"main.pkl","files":[{"path":"main.pkl","bytes":"%s","length":%d,"sha256":"%s"}]}`, base64.StdEncoding.EncodeToString(data), len(data), hex.EncodeToString(h[:]))
	call := func(api API, body, contentType string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/snapshots", strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	if got := call(API{}, body, "application/json").Code; got != 405 {
		t.Fatal(got)
	}
	api := API{Stager: st}
	for _, bad := range []string{strings.Replace(body, `"schema":`, `"schema":"dill.snapshot.v1","schema":`, 1), strings.Replace(body, `"length":13`, `"length":12`, 1), strings.Replace(body, `main.pkl`, `../main.pkl`, 1), strings.Replace(body, `"files":`, `"unknown":1,"files":`, 1), body + `{}`, strings.Repeat(" ", uploadMax+1)} {
		if w := call(api, bad, "application/json"); w.Code == 201 {
			t.Fatal("accepted malformed")
		}
	}
	if got := call(api, body, "text/plain").Code; got != 415 {
		t.Fatal(got)
	}
	st.slot <- struct{}{}
	if got := call(api, body, "application/json").Code; got != 429 {
		t.Fatal(got)
	}
	<-st.slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/v1/snapshots", bytes.NewBufferString(body)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("cancelled stage persisted")
	}
	w = call(api, body, "application/json")
	if w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), root) {
		t.Fatal("root leaked")
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	got, err := os.ReadFile(filepath.Join(root, entries[0].Name(), "main.pkl"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("bytes differ", err)
	}
}
