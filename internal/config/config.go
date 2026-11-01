// Code derived from dill.pkl. To regenerate from the schema, see tools/tools.go.
//
//go:generate pkl-gen-go generate --base-path github.com/umayr/dill ../../dill.pkl

package config

import "encoding/json"

type DillConfig struct {
	Name     string              `json:"name"`
	Engine   string              `json:"engine"`
	Services map[string]*Service `json:"services"`
}

type Service struct {
	// BaseDir is the directory of the config file that declared this service.
	// It is set programmatically after loading and is not part of the Pkl schema.
	// Relative bind-mount paths are resolved against it.
	BaseDir string `json:"-"`

	Image         string            `json:"image"`
	ContainerName string            `json:"containerName"`
	Restart       string            `json:"restart"`
	PullPolicy    string            `json:"pullPolicy"`
	Environment   map[string]string `json:"environment"`
	Volumes       []json.RawMessage `json:"volumes"` // string | Mount
	Ports         []json.RawMessage `json:"ports"`   // string | Port
	Labels        map[string]string `json:"labels"`
	DependsOn     []string          `json:"dependsOn"`
	Command       []string          `json:"command"`
	NetworkMode   string            `json:"networkMode"`
	HealthCheck   *HealthCheck      `json:"healthcheck"`
	User          string            `json:"user"`
	Init          *bool             `json:"init"`
	CapAdd        []string          `json:"capAdd"`
	Devices       []string          `json:"devices"`
	Cpuset        string            `json:"cpuset"`
}

// Port mirrors the Pkl Port class.
type Port struct {
	Target    int     `json:"target"`
	Published *int    `json:"published"`
	Protocol  string  `json:"protocol"`
	HostIP    *string `json:"hostIp"`
}

// Mount mirrors the Pkl Mount class.
type Mount struct {
	Type           string  `json:"type"`
	Source         *string `json:"source"`
	Target         string  `json:"target"`
	ReadOnly       bool    `json:"readOnly"`
	ExternalVolume bool    `json:"externalVolume"`
	Name           *string `json:"name"`
}

// HealthCheck mirrors the Pkl HealthCheck class.
// Duration fields (Interval, Timeout, StartPeriod) are kept as raw JSON because
// Pkl serializes Duration values as objects; callers should inspect and convert.
type HealthCheck struct {
	Test        json.RawMessage  `json:"test"` // List<String> | String
	Interval    *json.RawMessage `json:"interval"`
	Timeout     *json.RawMessage `json:"timeout"`
	Retries     *int             `json:"retries"`
	StartPeriod *json.RawMessage `json:"startPeriod"`
}
