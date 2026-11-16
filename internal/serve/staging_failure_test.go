package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/projectsnapshot"
)

func validUpload() string {
	b := []byte("secret")
	h := sha256.Sum256(b)
	return fmt.Sprintf(`{"schema":"dill.snapshot.v1","entrypoint":"private.pkl","files":[{"path":"private.pkl","bytes":"%s","length":6,"sha256":"%s"}]}`, base64.StdEncoding.EncodeToString(b), hex.EncodeToString(h[:]))
}
func TestCancelledReceiveReportsCleanupFailure(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := 0
	s.diagnostic = func() { diagnostic++ }
	s.receive = func(context.Context, string, projectsnapshot.Snapshot, projectsnapshot.Limits) (string, error) {
		return "", &projectsnapshot.CleanupError{Cause: context.Canceled, Cleanup: errors.New("private path secret")}
	}
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	if w.Code != 408 || diagnostic != 1 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("status %d diagnostic %d body %s", w.Code, diagnostic, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	dead := &disconnectedWriter{header: make(http.Header)}
	API{Stager: s}.ServeHTTP(dead, r)
	if dead.code != 408 || diagnostic != 2 {
		t.Fatalf("disconnected diagnostic: %d %d", dead.code, diagnostic)
	}
}
func TestUnidentifiedEntryDiagnosticOnDisconnect(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	s.receive = func(context.Context, string, projectsnapshot.Snapshot, projectsnapshot.Limits) (string, error) {
		return "", &projectsnapshot.CleanupError{Cause: errors.Join(context.Canceled, errors.New("private sentinel")), Cleanup: errors.New("secret sentinel")}
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := &disconnectedWriter{header: make(http.Header)}
	API{Stager: s}.ServeHTTP(w, r)
	if w.code != 408 || !strings.Contains(logs.String(), "snapshot staging cleanup failed; operator inspection required") || strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), root) {
		t.Fatalf("unexpected diagnostic: %d %s", w.code, logs.String())
	}
}
func TestUnknownPublicationResponseIsSanitized(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	s.receive = func(context.Context, string, projectsnapshot.Snapshot, projectsnapshot.Limits) (string, error) {
		return "", errors.Join(projectsnapshot.ErrPublicationUnknown, context.Canceled, errors.New("private sentinel"))
	}
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "outcome_unknown") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), root) || strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), root) {
		t.Fatalf("unsafe response %d %s logs=%s", w.Code, w.Body.String(), logs.String())
	}
}

func TestPublishedResponseHasOpaqueIdentifier(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || w.Code != 201 {
		t.Fatalf("%v %v %d", entries, err, w.Code)
	}
	id := strings.TrimPrefix(entries[0].Name(), "snapshot-")
	if len(id) != 32 || !strings.Contains(w.Body.String(), `"stage_id":"`+id+`"`) || strings.Contains(w.Body.String(), root) || strings.Contains(w.Body.String(), "snapshot-") {
		t.Fatalf("bad response %s", w.Body.String())
	}
}
func TestUploadLimits(t *testing.T) {
	if uploadMax != 24<<20 || stageLimits.Files != 1000 || stageLimits.FileBytes != 4<<20 || stageLimits.TotalBytes != 16<<20 || stageLimits.Depth != 32 || stageLimits.PathBytes != 512 || stageLimits.Directories != 255 {
		t.Fatal("unexpected limits")
	}
}
