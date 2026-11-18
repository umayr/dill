package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedHTTPSReadRoutes(t *testing.T) {
	const token = "synthetic-token"
	h := authenticatedReads(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), token)
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
