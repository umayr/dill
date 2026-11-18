package agentcmd

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/serve"
)

func TestAuthenticatedHTTPSReadRoutes(t *testing.T) {
	const token = "synthetic-token"
	h := authenticatedHTTPS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil, nil, token)
	for _, tt := range []struct {
		path   string
		auth   []string
		secure bool
		want   int
	}{
		{"/v1/inventory", nil, true, 401},
		{"/v1/inventory", []string{"Bearer wrong"}, true, 401},
		{"/v1/inventory", []string{"Bearer " + token, "Bearer " + token}, true, 401},
		{"/v1/inventory", []string{"Bearer " + token}, false, 401},
		{"/v1/inventory", []string{"Bearer " + token}, true, 204},
		{"/v1/reveal", []string{"Bearer " + token}, true, 404},
		{"/v1/deploy", []string{"Bearer " + token}, true, 404},
		{"/v1/deploy/preview", []string{"Bearer " + token}, true, 404},
		{"/v1/snapshots", []string{"Bearer " + token}, true, 404},
	} {
		r := httptest.NewRequest(http.MethodPost, tt.path, nil)
		if tt.secure {
			r.TLS = &tls.ConnectionState{}
		}
		for _, value := range tt.auth {
			r.Header.Add("Authorization", value)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("%s: got %d, want %d", tt.path, w.Code, tt.want)
		}
	}
}

func TestAuthenticatedHTTPSCataloguedStaging(t *testing.T) {
	base := t.TempDir()
	root, catalog := filepath.Join(base, "stages"), filepath.Join(base, "catalog")
	for _, p := range []string{root, catalog} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	stager, err := serve.NewCataloguedStager(root, catalog)
	if err != nil {
		t.Fatal(err)
	}
	defer stager.Close()
	h := authenticatedHTTPS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("staging unexpectedly called the engine-read handler")
	}), serve.API{Stager: stager}, nil, "synthetic-token")
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	upload := fmt.Sprintf(`{"schema":"dill.snapshot.v1","entrypoint":"demo.pkl","files":[{"path":"demo.pkl","bytes":"eA==","length":1,"sha256":"%x"}]}`, sha256.Sum256([]byte("x")))
	if w := request(http.MethodPost, "/v1/snapshots", "wrong", upload); w.Code != http.StatusUnauthorized {
		t.Fatal("upload accepted wrong token", w.Code)
	}
	w := request(http.MethodPost, "/v1/snapshots", "synthetic-token", upload)
	var result struct {
		ID string `json:"stage_id"`
	}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.ID) != 32 {
		t.Fatal("snapshot not catalogued", w.Code)
	}
	path := "/v1/snapshots/" + result.ID
	if w := request(http.MethodGet, path, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatal("manifest leaked without authentication", w.Code)
	}
	if w := request(http.MethodGet, path, "synthetic-token", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"manifest_sha256"`) {
		t.Fatal("catalogued manifest unavailable", w.Code)
	}
	for _, op := range []struct{ method, path string }{{http.MethodDelete, path}, {http.MethodGet, "/v1/health"}, {http.MethodPost, "/v1/deploy"}} {
		if w := request(op.method, op.path, "synthetic-token", ""); w.Code != http.StatusNotFound {
			t.Fatalf("unexpected route %s %s: %d", op.method, op.path, w.Code)
		}
	}
}
