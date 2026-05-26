package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ServiceConfigHash returns a stable fingerprint for the desired service spec
// after shorthand ports/volumes have been normalized.
func ServiceConfigHash(name string, svc *Service) (string, error) {
	ports, err := NormalizePorts(svc.Ports)
	if err != nil {
		return "", fmt.Errorf("normalizing ports: %w", err)
	}
	volumes, err := NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return "", fmt.Errorf("normalizing volumes: %w", err)
	}
	payload := struct {
		Name          string
		Image         string
		ContainerName string
		Restart       string
		Environment   map[string]string
		Volumes       []VolumeMount
		Ports         []PortBinding
		Labels        map[string]string
		Command       []string
		NetworkMode   string
		HealthCheck   *HealthCheck
		User          string
		Init          *bool
		CapAdd        []string
		Devices       []string
		Cpuset        string
	}{
		Name:          name,
		Image:         svc.Image,
		ContainerName: svc.ContainerName,
		Restart:       svc.Restart,
		Environment:   svc.Environment,
		Volumes:       volumes,
		Ports:         ports,
		Labels:        svc.Labels,
		Command:       svc.Command,
		NetworkMode:   svc.NetworkMode,
		HealthCheck:   svc.HealthCheck,
		User:          svc.User,
		Init:          svc.Init,
		CapAdd:        svc.CapAdd,
		Devices:       svc.Devices,
		Cpuset:        svc.Cpuset,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling service hash payload: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
