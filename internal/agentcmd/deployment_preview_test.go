package agentcmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

type stageGate func(context.Context, string, string, func(string) error) error

func (f stageGate) WithVerifiedEntrypoint(ctx context.Context, id, digest string, use func(string) error) error {
	return f(ctx, id, digest, use)
}

func TestHTTPSDeploymentPreviewAdmission(t *testing.T) {
	id, digest := strings.Repeat("1", 32), strings.Repeat("a", 64)
	doc := `{"stageId":"` + id + `","manifestSha256":"` + digest + `","stack":"demo"}`
	calls := 0
	preview := previewEndpoint{
		stages: stageGate(func(_ context.Context, gotID, gotDigest string, use func(string) error) error {
			if gotID != id || gotDigest != digest {
				t.Fatal("stage binding was not propagated")
			}
			return use("/trusted/stage/main.pkl")
		}),
		preview: func(_ context.Context, entrypoint, stack string) (preflight.Result, error) {
			calls++
			if entrypoint != "/trusted/stage/main.pkl" || stack != "demo" {
				t.Fatal("preview did not use trusted entrypoint and requested stack")
			}
			return preflight.Result{Stack: stack}, nil
		},
	}
	h := authenticatedHTTPS(nil, nil, preview, nil, "synthetic-monitor", "test-token", httpsDeployGrants{"preview": true})
	for _, tt := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"POST", "/v1/deploy/preview", "wrong", doc, 401},
		{"GET", "/v1/deploy/preview", "test-token", doc, 404},
		{"POST", "/v1/deploy/apply", "test-token", doc, 404},
		{"POST", "/v1/deploy/preview", "test-token", doc + "{}", 400},
		{"POST", "/v1/deploy/preview", "test-token", strings.Replace(doc, `"stack":"demo"`, `"stack":"demo","stack":"demo"`, 1), 400},
		{"POST", "/v1/deploy/preview", "test-token", strings.Replace(doc, `"stack":"demo"`, `"stack":"demo","engine":"podman"`, 1), 400},
		{"POST", "/v1/deploy/preview", "test-token", doc, 200},
	} {
		r := httptest.NewRequest(tt.method, "https://agent"+tt.path, strings.NewReader(tt.body))
		r.Header.Set("Authorization", "Bearer "+tt.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Errorf("preview returned %d, want %d", w.Code, tt.status)
		}
	}
	if calls != 1 {
		t.Fatalf("preview called %d times, want exactly once", calls)
	}
}

func TestHTTPSAuthorizedPreviewPreservesJournalChecks(t *testing.T) {
	history, err := openDeploymentHistory(historyDirectory(t), operation.Digest("synthetic target"))
	if err != nil {
		t.Fatal(err)
	}
	defer history.dir.Close()
	id, digest := strings.Repeat("1", 32), strings.Repeat("a", 64)
	doc := `{"stageId":"` + id + `","manifestSha256":"` + digest + `","stack":"demo"}`
	calls := 0
	managed := &applyEndpoint{history: history, previewEndpoint: previewEndpoint{
		stages: stageGate(func(_ context.Context, gotID, gotDigest string, use func(string) error) error {
			if gotID != id || gotDigest != digest {
				t.Fatal("managed preview lost snapshot binding")
			}
			return use("/synthetic/stage/main.pkl")
		}),
		preview: func(_ context.Context, _, stack string) (preflight.Result, error) {
			calls++
			return preflight.Result{Stack: stack, Resources: []preflight.Resource{{Presence: "absent"}}}, nil
		},
	}}
	unmanaged := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("managed preview fell back to the standalone handler")
	})
	h := authenticatedHTTPS(nil, nil, unmanaged, managed, "synthetic-monitor", "synthetic-deploy", httpsDeployGrants{"preview": true})
	request := func(token, journal string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://agent/v1/deploy/preview", strings.NewReader(doc))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(journalHeader, journal)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("synthetic-monitor", history.journal); w.Code != http.StatusForbidden || w.Header().Get(journalHeader) != "" || calls != 0 {
		t.Fatal("monitor request reached journal-aware preview", w.Code)
	}
	if w := request("synthetic-deploy", strings.Repeat("b", 32)); w.Code != http.StatusConflict || calls != 0 {
		t.Fatal("authorized preview bypassed journal check", w.Code)
	}
	w := request("synthetic-deploy", history.journal)
	if w.Code != http.StatusOK || w.Header().Get(journalHeader) != history.journal || w.Header().Get("X-Dill-Deployment-Mode") != "create" || calls != 1 {
		t.Fatal("authorized preview did not return managed journal metadata", w.Code)
	}
}

func TestCLIPreviewUsesReviewedTarget(t *testing.T) {
	fixture, err := os.ReadFile("../preflight/testdata/preview-up-create.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct{ Stdout preflight.Result }
	if err := json.Unmarshal(fixture, &golden); err != nil || golden.Stdout.Target == nil {
		t.Fatal("fixture unavailable")
	}
	result := golden.Stdout
	const socket = "unix:///run/example.sock"
	result.Target.EndpointDigest = operation.Digest(socket)
	body, err := result.Buffer(0)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	exe := filepath.Join(root, "fake-dill")
	program := `#!/bin/sh
set -eu
[ "$1" = "preflight" ] && [ "$2" = "up" ] && [ "$3" = "--contract" ] && [ "$4" = "dill.preflight/1" ]
[ "$8" = "$EXPECTED_STACK" ] || exit 9
[ "$DILL_PKL_PATH" = /trusted/pkl ]
cat <<'RESULT'
` + string(body) + "RESULT\n"
	// The child gets no inherited environment; use an observed literal stack
	// rather than pass it as a new environment variable or a shell input.
	program = strings.ReplaceAll(program, `"$EXPECTED_STACK"`, `"`+result.Stack+`"`)
	if err := os.WriteFile(exe, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "main.pkl")
	if _, err := cliPreview(exe, "/trusted/pkl", socket, "daemon", "/state")(context.Background(), entry, result.Stack); err != nil {
		t.Fatal(err)
	}
	if _, err := cliPreview(exe, "/trusted/pkl", "unix:///run/other.sock", "daemon", "/state")(context.Background(), entry, result.Stack); err == nil {
		t.Fatal("accepted receipt for another target")
	}
}
