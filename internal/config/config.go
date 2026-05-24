package config

type DillConfig struct {
	Version  string              `pkl:"version"`
	Engine   string              `pkl:"engine"`
	Services map[string]*Service `pkl:"services"`
}

type Service struct {
	Image         string            `pkl:"image"`
	ContainerName string            `pkl:"container_name"`
	Restart       string            `pkl:"restart"`
	Environment   map[string]string `pkl:"environment"`
	Volumes       []string          `pkl:"volumes"`
	Ports         []string          `pkl:"ports"`
	DependsOn     []string          `pkl:"depends_on"`
}
