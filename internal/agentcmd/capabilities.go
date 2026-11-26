package agentcmd

import (
	"encoding/json"
	"net/http"
	"sort"
)

// This metadata document describes configured routes, not execution outcomes or
// authority granted to its reader. Monitor and deployment credentials stay separate.
func writeHTTPSCapabilities(w http.ResponseWriter, backends []http.Handler, deployEnabled bool, grants httpsDeployGrants) {
	deployment := []string{}
	seen := map[string]bool{}
	if deployEnabled {
		for _, route := range httpsRoutes {
			if route.backend != httpsMonitor && backends[route.backend] != nil && grants[route.grant] && !seen[route.grant] {
				deployment = append(deployment, route.grant)
				seen[route.grant] = true
			}
		}
	}
	sort.Strings(deployment)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Schema     string   `json:"schema"`
		Monitor    []string `json:"monitor"`
		Deployment []string `json:"deployment_grants"`
	}{"dill.agent.capabilities.v1", []string{"capabilities", "inspect", "inventory", "logs", "session"}, deployment})
}
