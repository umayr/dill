package agentcmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/hostagent"
)

func TestDockerListIncrementalLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "["+strings.Repeat(`{},`, 1000)+`{}]`)
	}))
	defer srv.Close()
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	d := dockerIdentity{client: &http.Client{Transport: tr}}
	v, e := d.InventoryList(context.Background())
	if !errors.Is(e, hostagent.ErrLimit) || v != nil {
		t.Fatalf("limit: %v %v", v, e)
	}
}
