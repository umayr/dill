package agentcmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestHTTPSCapabilitiesAreMetadataNotCredentialAuthority(t *testing.T) {
	backend := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("capability discovery invoked an operation backend") })
	handler := authenticatedHTTPS(backend, backend, nil, nil, "synthetic-monitor", "synthetic-deployer", httpsDeployGrants{"stage": true, "apply": true})
	for _, tc := range []struct {
		auth string
		want int
	}{{"", 401}, {"Bearer wrong", 401}, {"Bearer synthetic-deployer", 403}, {"Bearer synthetic-monitor", 200}} {
		req := httptest.NewRequest("GET", "https://target/v1/capabilities", nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want)
		}
		if tc.want == 200 {
			var doc struct {
				Schema     string   `json:"schema"`
				Monitor    []string `json:"monitor"`
				Deployment []string `json:"deployment_grants"`
			}
			if json.Unmarshal(w.Body.Bytes(), &doc) != nil || doc.Schema != "dill.agent.capabilities.v1" || !reflect.DeepEqual(doc.Deployment, []string{"stage"}) {
				t.Fatal("reported disabled backend or missing grant", w.Body.String())
			}
			if len(doc.Monitor) != 5 {
				t.Fatal("monitor route report incomplete")
			}
		}
	}
}
func TestHTTPSCapabilitiesDoNotInferDeployFromMonitor(t *testing.T) {
	backend := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("backend invoked") })
	h := authenticatedHTTPS(backend, backend, nil, nil, "same", "same", httpsDeployGrants{"stage": true})
	req := httptest.NewRequest("GET", "https://target/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer same")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var doc struct {
		Deployment []string `json:"deployment_grants"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &doc) != nil || len(doc.Deployment) != 0 {
		t.Fatal("duplicate credential advertised deployment authority")
	}
}
