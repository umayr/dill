package agentcmd

import (
	"errors"
	"os"
	"path/filepath"
)

func checkDeployGrantBackends(grants httpsDeployGrants, staging, preview, apply bool) error {
	for name, enabled := range grants {
		if !enabled {
			continue
		}
		found := false
		for _, route := range httpsRoutes {
			if route.grant != name {
				continue
			}
			found = true
			ready := route.backend == httpsStaging && staging || route.backend == httpsPreview && preview || route.backend == httpsApply && apply
			if !ready {
				return errors.New("HTTPS deploy grant requires its configured backend")
			}
		}
		if !found {
			return errors.New("unknown HTTPS deploy grant")
		}
	}
	return nil
}

// Spellings, symlink aliases and bind aliases must not collapse journal history
// into coordination, staging, catalog or credentials. Existing backend openers
// still perform their own no-follow/private-directory validation afterward.
func separateDeploymentHistory(path string, peers ...string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Stat(path)
	// History already requires an existing private directory at open. Require
	// it here too, before any directory alias can become an active backend.
	if err != nil || !info.IsDir() {
		return false
	}
	for _, peer := range peers {
		if peer == "" {
			continue
		}
		if filepath.Clean(peer) == path {
			return false
		}
		other, e := os.Stat(peer)
		if e != nil && !os.IsNotExist(e) {
			return false
		}
		if err == nil && e == nil && os.SameFile(info, other) {
			return false
		}
	}
	return true
}
