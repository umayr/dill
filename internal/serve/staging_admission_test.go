//go:build linux || darwin

package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCataloguedUploadAtAggregateCapacity(t *testing.T) {
	base := t.TempDir()
	root, records := filepath.Join(base, "stages"), filepath.Join(base, "records")
	for _, dir := range []string{root, records} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const stageCapacity = 64
	// Empty abandoned trees isolate the stage-count budget without large data.
	for i := 0; i < stageCapacity-1; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf(".in-progress-%032x", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewCataloguedStager(root, records)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api := API{Stager: s}
	post := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/snapshots", strings.NewReader(validUpload()))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	first := post()
	var stage struct {
		ID string `json:"stage_id"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &stage); first.Code != http.StatusCreated || err != nil || len(stage.ID) != 32 {
		t.Fatalf("last available slot: %d %q (%v)", first.Code, first.Body, err)
	}
	w := post()
	if w.Code != http.StatusInsufficientStorage || !strings.Contains(w.Body.String(), `"staging_capacity"`) || strings.Contains(w.Body.String(), root) || strings.Contains(w.Body.String(), stage.ID) || strings.Contains(w.Body.String(), "private.pkl") {
		t.Fatalf("exhausted admission: %d %q", w.Code, w.Body)
	}
	if stages, err := os.ReadDir(root); err != nil || len(stages) != stageCapacity {
		t.Fatalf("rejected upload modified staging root: %d entries (%v)", len(stages), err)
	}
	if entries, err := os.ReadDir(records); err != nil || len(entries) != 1 || entries[0].Name() != stage.ID {
		t.Fatalf("rejected upload modified catalog: %v (%v)", entries, err)
	}
	w = httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/snapshots/"+stage.ID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("previous stage unavailable: %d %q", w.Code, w.Body)
	}
}
