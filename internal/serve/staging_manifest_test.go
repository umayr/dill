package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestLargeEscapedPaths(t *testing.T) {
	base := t.TempDir()
	root, records := filepath.Join(base, "root"), filepath.Join(base, "records")
	for _, p := range []string{root, records} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hash := sha256.Sum256(nil)
	type file struct {
		Path   string `json:"path"`
		Bytes  string `json:"bytes"`
		Length int    `json:"length"`
		SHA    string `json:"sha256"`
	}
	files := make([]file, 1000)
	for i := range files {
		files[i] = file{Path: strings.Repeat("&", 250) + "/" + fmt.Sprintf("%04d", i) + strings.Repeat("&", 246), SHA: hex.EncodeToString(hash[:])}
	}
	body, err := json.Marshal(struct {
		Schema     string `json:"schema"`
		Entrypoint string `json:"entrypoint"`
		Files      []file `json:"files"`
	}{"dill.snapshot.v1", files[0].Path, files})
	if err != nil {
		t.Fatal(err)
	}
	a := API{Stager: s}
	req := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)
	var result struct {
		ID string `json:"stage_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 201 {
		t.Fatalf("upload: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/v1/snapshots/"+result.ID, nil))
	var detail struct {
		Manifest json.RawMessage `json:"manifest"`
		Digest   string          `json:"manifest_sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || w.Body.Len() <= maxResponse || w.Body.Len() > manifestResponseLimit {
		t.Fatalf("response: %d len=%d err=%v", w.Code, w.Body.Len(), err)
	}
	sum := sha256.Sum256(detail.Manifest)
	if hex.EncodeToString(sum[:]) != detail.Digest {
		t.Fatal("escaped manifest digest mismatch")
	}
}

func TestManifestCapabilityAndBounds(t *testing.T) {
	base := t.TempDir()
	root, records := filepath.Join(base, "root"), filepath.Join(base, "records")
	for _, p := range []string{root, records} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	testRequest := func(a API, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	id := strings.Repeat("1", 32)
	if w := testRequest(API{Stager: legacy}, "GET", "/v1/snapshots/"+id, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := testRequest(API{Stager: legacy}, "GET", "/v1/health", ""); strings.Contains(w.Body.String(), "snapshot-manifest") {
		t.Fatal(w.Body.String())
	}
	stager, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer stager.Close()
	api := API{Stager: stager}
	if w := testRequest(api, "GET", "/v1/health", ""); !strings.Contains(w.Body.String(), "snapshot-manifest") {
		t.Fatal(w.Body.String())
	}
	if w := testRequest(api, "GET", "/v1/snapshots/"+id, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := testRequest(api, "GET", "/v1/snapshots/not-an-id", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := testRequest(api, "POST", "/v1/snapshots/"+id, ""); w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	stager.slot <- struct{}{}
	if w := testRequest(api, "GET", "/v1/snapshots/"+id, ""); w.Code != 429 {
		t.Fatal(w.Code, w.Body.String())
	}
	<-stager.slot
	upload := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	upload.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, upload)
	var stage struct {
		ID string `json:"stage_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &stage); err != nil || response.Code != 201 {
		t.Fatalf("upload: %d %s %v", response.Code, response.Body.String(), err)
	}
	w := testRequest(api, "GET", "/v1/snapshots/"+stage.ID, "")
	var payload struct {
		Manifest json.RawMessage `json:"manifest"`
		Digest   string          `json:"manifest_sha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != 200 {
		t.Fatalf("manifest %d %s %v", w.Code, w.Body.String(), err)
	}
	sum := sha256.Sum256(payload.Manifest)
	if hex.EncodeToString(sum[:]) != payload.Digest {
		t.Fatalf("manifest bytes do not match digest: %s", w.Body.String())
	}
}
