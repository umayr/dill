package agentcmd

import (
	"encoding/json"
	"github.com/umayr/dill/internal/operation"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeploymentJournalDiscoveryRequiresSeparateGrant(t *testing.T) {
	h, e := openDeploymentHistory(historyDirectory(t), operation.Digest("target"))
	if e != nil {
		t.Fatal(e)
	}
	defer h.dir.Close()
	endpoint := &applyEndpoint{history: h}
	for _, tc := range []struct {
		token string
		grant bool
		body  string
		want  int
	}{{"monitor", true, "", 403}, {"deploy", false, "", 403}, {"bad", true, "", 401}, {"deploy", true, "", 200}, {"deploy", true, "body", 400}} {
		routes := authenticatedHTTPS(nil, nil, nil, endpoint, "monitor", "deploy", httpsDeployGrants{"journal": tc.grant})
		r := httptest.NewRequest("GET", "https://agent/v1/deploy/journal", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code)
		}
		if w.Code == 200 {
			var doc map[string]string
			if json.Unmarshal(w.Body.Bytes(), &doc) != nil || len(doc) != 1 || doc["journalId"] != h.journal || w.Header().Get(journalHeader) != h.journal {
				t.Fatal("bad journal evidence")
			}
		}
	}
	if len(h.rows) != 0 {
		t.Fatal("discovery mutated deployment history")
	}
}
