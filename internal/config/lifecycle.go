package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Job is the restricted, one-shot workload specification.
type Job struct {
	Image       string            `json:"image"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment"`
	Volumes     []json.RawMessage `json:"volumes"`
	User        string            `json:"user"`
	PullPolicy  string            `json:"pullPolicy"`
	DependsOn   []string          `json:"dependsOn"`
	AfterJobs   []string          `json:"afterJobs"`
}

// Hook names a job and optionally restricts an up event to a change kind.
type Hook struct {
	Job      string `json:"job"`
	OnChange string `json:"onChange,omitempty"`
}

// MatchedJobs returns jobs in declaration order for an event and change.
// Create and update aliases correspond to create and replace respectively.
func MatchedJobs(h *LifecycleHooks, phase, change string) []string {
	if h == nil {
		return nil
	}
	var canonical, alias []Hook
	switch phase {
	case "beforeUp":
		canonical = h.BeforeUp
		if change == "create" {
			alias = h.BeforeCreate
		} else if change == "replace" {
			alias = h.BeforeUpdate
		}
	case "afterUp":
		canonical = h.AfterUp
		if change == "create" {
			alias = h.AfterCreate
		} else if change == "replace" {
			alias = h.AfterUpdate
		}
	case "beforeDown":
		canonical = h.BeforeDown
	case "afterDown":
		canonical = h.AfterDown
	default:
		return nil
	}
	var jobs []string
	for _, hook := range canonical {
		if phase == "beforeDown" || phase == "afterDown" || ((change == "create" || change == "replace") && (hook.OnChange == "" || hook.OnChange == "createOrReplace" || hook.OnChange == change)) {
			jobs = append(jobs, hook.Job)
		}
	}
	for _, hook := range alias {
		jobs = append(jobs, hook.Job)
	}
	return jobs
}

// LifecycleHooks holds the canonical events and their compatibility aliases.
type LifecycleHooks struct {
	BeforeUp     []Hook `json:"beforeUp"`
	AfterUp      []Hook `json:"afterUp"`
	BeforeDown   []Hook `json:"beforeDown"`
	AfterDown    []Hook `json:"afterDown"`
	BeforeCreate []Hook `json:"beforeCreate"`
	AfterCreate  []Hook `json:"afterCreate"`
	BeforeUpdate []Hook `json:"beforeUpdate"`
	AfterUpdate  []Hook `json:"afterUpdate"`
}

func validateLifecycle(cfg *DillConfig) error {
	edges := map[string][]string{}
	for name, j := range cfg.Jobs {
		if j == nil {
			return fmt.Errorf("job %q is nil", name)
		}
		if strings.TrimSpace(j.Image) == "" || len(j.Command) == 0 || strings.TrimSpace(j.Command[0]) == "" {
			return fmt.Errorf("job %q requires image and command", name)
		}
		if len(j.Volumes) > 0 {
			return fmt.Errorf("job %q: volumes are unsupported", name)
		}
		if j.PullPolicy != "" && j.PullPolicy != "always" && j.PullPolicy != "missing" && j.PullPolicy != "never" {
			return fmt.Errorf("job %q: invalid pullPolicy", name)
		}
		for _, dep := range j.DependsOn {
			if cfg.Services[dep] == nil {
				return fmt.Errorf("job %q: unknown service %q", name, dep)
			}
			edges["j:"+name] = append(edges["j:"+name], "s:"+dep)
		}
		for _, dep := range j.AfterJobs {
			if cfg.Jobs[dep] == nil {
				return fmt.Errorf("job %q: unknown afterJobs job %q", name, dep)
			}
			edges["j:"+name] = append(edges["j:"+name], "j:"+dep)
		}
	}
	for name, svc := range cfg.Services {
		if svc == nil {
			continue
		}
		for _, dep := range svc.DependsOn {
			edges["s:"+name] = append(edges["s:"+name], "s:"+dep)
		}
		if svc.Hooks == nil {
			continue
		}
		h := svc.Hooks
		groups := []struct {
			name  string
			hooks []Hook
			up    bool
		}{{"beforeUp", h.BeforeUp, true}, {"afterUp", h.AfterUp, true}, {"beforeDown", h.BeforeDown, false}, {"afterDown", h.AfterDown, false}, {"beforeCreate", h.BeforeCreate, false}, {"afterCreate", h.AfterCreate, false}, {"beforeUpdate", h.BeforeUpdate, false}, {"afterUpdate", h.AfterUpdate, false}}
		for _, g := range groups {
			for _, hook := range g.hooks {
				if cfg.Jobs[hook.Job] == nil {
					return fmt.Errorf("service %q %s: unknown job %q", name, g.name, hook.Job)
				}
				if g.up {
					if hook.OnChange != "" && hook.OnChange != "createOrReplace" && hook.OnChange != "create" && hook.OnChange != "replace" {
						return fmt.Errorf("service %q %s: invalid onChange %q", name, g.name, hook.OnChange)
					}
				} else if hook.OnChange != "" {
					return fmt.Errorf("service %q %s: onChange is unsupported", name, g.name)
				}
			}
		}
		for _, phase := range []string{"beforeUp", "afterUp", "beforeDown", "afterDown"} {
			for _, change := range []string{"create", "replace"} {
				seen := map[string]bool{}
				for _, job := range MatchedJobs(h, phase, change) {
					if seen[job] {
						return fmt.Errorf("service %q %s: overlapping job %q", name, phase, job)
					}
					seen[job] = true
					if phase == "beforeUp" {
						edges["s:"+name] = append(edges["s:"+name], "j:"+job)
					}
				}
			}
		}
	}
	if len(cfg.Jobs) == 0 {
		return nil
	} // Preserve ordinary service graph diagnostics.
	state := map[string]int{}
	var visit func(string) error
	visit = func(n string) error {
		if state[n] == 1 {
			return fmt.Errorf("lifecycle dependency cycle involving %s", n)
		}
		if state[n] == 2 {
			return nil
		}
		state[n] = 1
		for _, d := range edges[n] {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[n] = 2
		return nil
	}
	for n := range edges {
		if err := visit(n); err != nil {
			return err
		}
	}
	return nil
}
