package serve

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/projectsnapshot"
)

// Cancel once the receiver has created an in-progress directory.
type cancelWhenCreated struct {
	context.Context
	root string
}

func (c cancelWhenCreated) Err() error {
	es, err := os.ReadDir(c.root)
	if err == nil && len(es) > 0 {
		return context.Canceled
	}
	return c.Context.Err()
}
func TestCancellationAfterCreationReturnsFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := NewStager(root)
	if err != nil {
		t.Fatal(err)
	}
	s.receive = func(ctx context.Context, root string, snap projectsnapshot.Snapshot, limits projectsnapshot.Limits) (string, error) {
		return projectsnapshot.Receive(cancelWhenCreated{ctx, root}, root, snap, limits)
	}
	r := httptest.NewRequest("POST", "/v1/snapshots", strings.NewReader(validUpload()))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	API{Stager: s}.ServeHTTP(w, r)
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 || w.Code != 408 {
		t.Fatalf("status %d entries %v error %v", w.Code, entries, err)
	}
}
