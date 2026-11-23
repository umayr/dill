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
