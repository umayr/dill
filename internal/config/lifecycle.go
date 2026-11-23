package config

import "encoding/json"

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
