package agentcmd

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/hostagent"
	"github.com/umayr/dill/internal/serve"
)

func TestAuthenticatedHTTPSReadRoutes(t *testing.T) {
	const token = "synthetic-token"
	h := authenticatedHTTPS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil, nil, nil, token, "", nil)
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
		{"/v1/deploy/apply", []string{"Bearer " + token}, true, 404},
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

func TestAuthenticatedHTTPSDeployGrants(t *testing.T) {
	const monitor, deploy = "synthetic-monitor", "synthetic-deploy"
	const id = "0123456789abcdef0123456789abcdef"
	type endpoint struct{ method, path, grant, backend string }
	reads := []endpoint{
		{"POST", "/v1/session", "", "monitor"},
		{"POST", "/v1/inventory", "", "monitor"},
		{"POST", "/v1/inspect", "", "monitor"},
		{"POST", "/v1/logs", "", "monitor"},
	}
	writes := []endpoint{
		{"POST", "/v1/snapshots", "stage", "staging"},
		{"GET", "/v1/snapshots/" + id, "manifest", "staging"},
		{"POST", "/v1/deploy/preview", "preview", "apply"},
		{"POST", "/v1/deploy/apply", "apply", "apply"},
		{"POST", "/v1/deploy/journal-apply", "journal-apply", "apply"},
		{"POST", "/v1/deploy/remove-preview", "remove-preview", "apply"},
		{"POST", "/v1/deploy/remove", "remove", "apply"},
		{"POST", "/v1/deploy/fence", "fence", "apply"},
		{"GET", "/v1/deploy/operations/" + id, "status", "apply"},
	}
	var called []string
	spy := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = append(called, name)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	check := func(h http.Handler, ep endpoint, token string, status int, backend string) {
		t.Helper()
		called = nil
		w := requestHTTPS(h, ep.method, ep.path, token)
		if w.Code != status || strings.Join(called, ",") != backend || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: got %d (called %v), want %d (called %s)", ep.method, ep.path, w.Code, called, status, backend)
		}
	}
	build := func(m, d string, g httpsDeployGrants) http.Handler {
		return authenticatedHTTPS(spy("monitor"), spy("staging"), spy("preview"), spy("apply"), m, d, g)
	}
	all := make(httpsDeployGrants)
	for _, ep := range writes {
		if err := all.add(ep.grant); err != nil {
			t.Fatal(err)
		}
	}
	full := build(monitor, deploy, all)
	for _, ep := range reads {
		check(full, ep, monitor, 204, ep.backend)
		check(full, ep, deploy, 403, "")
		check(full, ep, "revoked", 401, "")
	}
	for _, ep := range writes {
		check(full, ep, monitor, 403, "")
		check(full, ep, "revoked", 401, "")
		check(build(monitor, "", all), ep, monitor, 404, "")
		check(build(monitor, monitor, all), ep, monitor, 404, "")
		check(build(monitor, deploy, nil), ep, deploy, 403, "")
	}
	for _, enabled := range writes {
		t.Run(enabled.grant, func(t *testing.T) {
			onlyOne := build(monitor, deploy, httpsDeployGrants{enabled.grant: true})
			for _, ep := range writes {
				if ep.grant == enabled.grant {
					check(onlyOne, ep, deploy, 204, ep.backend)
				} else {
					check(onlyOne, ep, deploy, 403, "")
				}
			}
		})
	}
	for _, ep := range []endpoint{
		{method: "POST", path: "/v1/reveal"},
		{method: "POST", path: "/v1/capabilities"},
		{method: "POST", path: "/v1/metrics"},
		{method: "GET", path: "/v1/health"},
		{method: "GET", path: "/v1/status"},
		{method: "GET", path: "/v1/deploy/preview"},
		{method: "GET", path: "/v1/deploy/operations/"},
		{method: "GET", path: "/v1/snapshots/" + id + "/more"},
		{method: "POST", path: "/v1/deploy/%70review"},
	} {
		check(full, ep, monitor, 404, "")
		check(full, ep, deploy, 404, "")
	}
}

func requestHTTPS(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://test"+path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthenticatedHTTPSRestartUsesOnlyCurrentCredentials(t *testing.T) {
	const monitorA, monitorB, deployA, deployB = "monitor-A", "monitor-B", "deploy-A", "deploy-B"
	reply := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	boot := func(m, d string) http.Handler {
		return authenticatedHTTPS(reply, nil, nil, reply, m, d, httpsDeployGrants{"status": true})
	}
	const status = "/v1/deploy/operations/0123456789abcdef0123456789abcdef"
	for _, tt := range []struct {
		monitor, deploy, method, path, token string
		want                                 int
	}{
		{monitorA, deployA, "POST", "/v1/inventory", monitorA, 204},
		{monitorA, deployA, "GET", status, deployA, 204},
		{monitorB, deployA, "POST", "/v1/inventory", monitorA, 401},
		{monitorB, deployA, "POST", "/v1/inventory", monitorB, 204},
		{monitorB, deployA, "GET", status, deployA, 204},
		{monitorA, deployB, "GET", status, deployA, 401},
		{monitorA, deployB, "GET", status, deployB, 204},
		{monitorA, deployB, "POST", "/v1/inventory", monitorA, 204},
		{monitorA, "", "GET", status, deployA, 401},
	} {
		if w := requestHTTPS(boot(tt.monitor, tt.deploy), tt.method, tt.path, tt.token); w.Code != tt.want {
			t.Errorf("%s %s after credential replacement: got %d, want %d", tt.method, tt.path, w.Code, tt.want)
		}
	}
}

type testDaemonID string

func (id testDaemonID) DaemonID(context.Context) (string, error) { return string(id), nil }

func TestHTTPSMonitorKeepsEnrollmentAndSessionChecks(t *testing.T) {
	const monitor, deploy = "synthetic-monitor", "synthetic-deploy"
	cfg := hostagent.Config{EngineID: "11111111-1111-4111-8111-111111111111", Generation: 1, EngineKind: "docker",
		EngineSocket: "unix:///synthetic/docker.sock", DaemonID: "daemon", PrivateKey: ed25519.NewKeyFromSeed(make([]byte, 32)), Identity: testDaemonID("daemon")}
	boot := func(cfg hostagent.Config) http.Handler {
		t.Helper()
		server, err := hostagent.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return authenticatedHTTPS(server, nil, nil, nil, monitor, deploy, nil)
	}
	call := func(h http.Handler, route, token, binding, epoch, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://test/v1/"+route, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		r.Header.Set("X-Brine-Binding", binding)
		r.Header.Set("X-Agent-Epoch", epoch)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	h := boot(cfg)
	session := `{"nonce":"` + base64.RawURLEncoding.EncodeToString(make([]byte, 32)) + `"}`
	w := call(h, "session", monitor, "1", "", session)
	var result struct{ Data struct{ Epoch string } }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Data.Epoch == "" {
		t.Fatal("monitor could not establish enrolled session", w.Code)
	}
	if w := call(h, "session", monitor, "2", "", session); w.Code != 409 {
		t.Fatal("wrong binding generation accepted", w.Code)
	}
	cfg.Identity = testDaemonID("wrong-daemon")
	if w := call(boot(cfg), "session", monitor, "1", "", session); w.Code != 409 {
		t.Fatal("wrong daemon identity accepted", w.Code)
	}
	cfg.Identity = testDaemonID("daemon")
	restarted := boot(cfg)
	if w := call(restarted, "inventory", monitor, "1", result.Data.Epoch, `{}`); w.Code != 403 || !strings.Contains(w.Body.String(), `"code":"UNAUTHORIZED"`) {
		t.Fatal("stale monitor epoch was not rejected by the session handler", w.Code)
	}
	if w := call(restarted, "inventory", deploy, "1", result.Data.Epoch, `{}`); w.Code != 403 || w.Body.String() != "forbidden\n" {
		t.Fatal("deploy credential reached the monitor session handler", w.Code)
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
	}), serve.API{Stager: stager}, nil, nil, "synthetic-monitor", "synthetic-token", httpsDeployGrants{"stage": true, "manifest": true})
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
		t.Fatal("upload accepted invalid token", w.Code)
	}
	if w := request(http.MethodPost, "/v1/snapshots", "synthetic-monitor", upload); w.Code != http.StatusForbidden {
		t.Fatal("upload accepted monitor token", w.Code)
	}
	for _, dir := range []string{root, catalog} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("unauthorized upload modified staging or catalog", err)
		}
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
		t.Fatal("manifest leaked without a token", w.Code)
	}
	if w := request(http.MethodGet, path, "synthetic-monitor", ""); w.Code != http.StatusForbidden {
		t.Fatal("manifest leaked without its deploy grant", w.Code)
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
