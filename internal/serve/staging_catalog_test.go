//go:build linux || darwin

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/projectsnapshot"
)

func TestCataloguedStagerPublishesBoundManifest(t *testing.T) {
	root, records := t.TempDir(), t.TempDir()
	for _, dir := range []string{root, records} {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest(http.MethodPost, "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	var body struct {
		Status string `json:"status"`
		ID     string `json:"stage_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusCreated || body.Status != "staged" || len(body.ID) != 32 {
		t.Fatalf("response=%d %s err=%v", w.Code, w.Body.String(), err)
	}
	manifest, err := s.catalog.Lookup(context.Background(), body.ID)
	if err != nil || manifest.Entrypoint != "private.pkl" || len(manifest.Files) != 1 || manifest.Files[0].Path != "private.pkl" || manifest.Files[0].Length != 6 {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	if _, err = os.Stat(filepath.Join(root, "snapshot-"+body.ID, "private.pkl")); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/snapshots/"+body.ID, nil)
	response := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(response, request)
	var detail struct {
		ID       string                   `json:"stage_id"`
		Manifest projectsnapshot.Manifest `json:"manifest"`
		Digest   string                   `json:"manifest_sha256"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil || response.Code != http.StatusOK || detail.ID != body.ID || detail.Manifest.Entrypoint != "private.pkl" || len(detail.Digest) != 64 || strings.Contains(response.Body.String(), root) || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("detail=%d %s err=%v", response.Code, response.Body.String(), err)
	}
	if err := os.WriteFile(filepath.Join(root, "snapshot-"+body.ID, "private.pkl"), []byte("tamper"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/snapshots/"+body.ID, nil))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "tamper") {
		t.Fatalf("tampered stage returned %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogFailureRetainsPublishedStageAndReturnsUnknown(t *testing.T) {
	root, records := t.TempDir(), t.TempDir()
	for _, dir := range []string{root, records} {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	receive := s.receive
	s.receive = func(ctx context.Context, root string, snap projectsnapshot.Snapshot, limits projectsnapshot.Limits) (string, error) {
		path, err := receive(ctx, root, snap, limits)
		if err == nil {
			if closeErr := s.catalog.Close(); closeErr != nil {
				return "", closeErr
			}
		}
		return path, err
	}
	defer s.Close()
	r := httptest.NewRequest(http.MethodPost, "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	entries, err := os.ReadDir(root)
	if err != nil || w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "outcome_unknown") || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "snapshot-") {
		t.Fatalf("status=%d body=%s entries=%v err=%v", w.Code, w.Body.String(), entries, err)
	}
	b, err := os.ReadFile(filepath.Join(root, entries[0].Name(), "private.pkl"))
	if err != nil || string(b) != "secret" {
		t.Fatalf("stage lost: %q %v", b, err)
	}
}

func TestCataloguedStagerRejectsUnsafeRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCataloguedStager(root, root); err == nil {
		t.Fatal("same root accepted")
	}
	// Existing uncatalogued stages are not adopted as a side effect of opening.
	records := t.TempDir()
	if err := os.Chmod(records, 0700); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if err := os.Mkdir(filepath.Join(root, "snapshot-"+id), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.catalog.Lookup(context.Background(), id); err != projectsnapshot.ErrStageUnknown {
		t.Fatalf("foreign stage lookup: %v", err)
	}
}
