package serve

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentDormantByDefault(t *testing.T) {
	a := API{}
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/v1/deployments", nil)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code == 200 {
			t.Fatalf("route active: %s", method)
		}
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/v1/health", nil))
	if strings.Contains(w.Body.String(), "deployment") {
		t.Fatal(w.Body.String())
	}
}
