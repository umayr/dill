package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// DownHookLabel commits a managed container to its applied down-hook definitions.
const DownHookLabel = "dill.down-hooks-sha256"

// DownHookDigest commits to the down hooks and the complete transitive job graph.
// An empty digest means the service has no down hooks.
func DownHookDigest(cfg *DillConfig, service string) (string, error) {
	if cfg == nil || cfg.Services[service] == nil {
		return "", fmt.Errorf("unknown service %q", service)
	}
	hooks := cfg.Services[service].Hooks
	if hooks == nil || (len(hooks.BeforeDown) == 0 && len(hooks.AfterDown) == 0) {
		return "", nil
	}
	jobs := map[string]*Job{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("job dependency cycle at %q", name)
		}
		if _, ok := jobs[name]; ok {
			return nil
		}
		job := cfg.Jobs[name]
		if job == nil {
			return fmt.Errorf("unknown job %q", name)
		}
		visiting[name] = true
		for _, dep := range job.AfterJobs {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, name)
		jobs[name] = job
		return nil
	}
	for _, h := range append(append([]Hook(nil), hooks.BeforeDown...), hooks.AfterDown...) {
		if err := visit(h.Job); err != nil {
			return "", err
		}
	}
	data, err := json.Marshal(struct {
		Version int             `json:"version"`
		Before  []Hook          `json:"before"`
		After   []Hook          `json:"after"`
		Jobs    map[string]*Job `json:"jobs"`
	}{1, hooks.BeforeDown, hooks.AfterDown, jobs})
	if err != nil {
		return "", fmt.Errorf("encoding down hooks: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
