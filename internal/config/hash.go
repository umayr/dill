package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ServiceConfigHash returns a stable fingerprint for the desired service spec
// after shorthand ports/volumes have been normalized and stack-scoped volume
// names have been resolved.
func ServiceConfigHash(stackName, name string, svc *Service) (string, error) {
	if svc == nil || len(svc.Secrets) > 0 {
		return "", ErrSecretUnavailable
	}
	b, err := serviceHashPayload(stackName, name, svc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func serviceHashPayload(stackName, name string, svc *Service) ([]byte, error) {
	ports, err := NormalizePorts(svc.Ports)
	if err != nil {
		return nil, fmt.Errorf("normalizing ports: %w", err)
	}
	volumes, err := NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("normalizing volumes: %w", err)
	}
	resolved := ResolveVolumes(volumes, stackName)
	resources, err := NormalizeResources(svc)
	if err != nil {
		return nil, fmt.Errorf("normalizing resources: %w", err)
	}
	var resourcePtr *ResourceLimits
	if !resources.IsZero() {
		resourcePtr = &resources
	}
	type hashVolume struct {
		Type     string
		Source   string
		Target   string
		ReadOnly bool
	}
	hashVolumes := make([]hashVolume, len(resolved))
	for i, v := range resolved {
		hashVolumes[i] = hashVolume{Type: v.Type, Source: v.Source, Target: v.Target, ReadOnly: v.ReadOnly}
	}
	payload := struct {
		Name          string
		Image         string
		ContainerName string
		Restart       string
		Environment   map[string]string
		Volumes       []hashVolume
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
		Resources     *ResourceLimits `json:"resources,omitempty"`
	}{
		Name:          name,
		Image:         svc.Image,
		ContainerName: svc.ContainerName,
		Restart:       svc.Restart,
		Environment:   svc.Environment,
		Volumes:       hashVolumes,
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
		Resources:     resourcePtr,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling service hash payload: %w", err)
	}
	return b, nil
}

// SecretServiceConfigHash extends the legacy payload with references only.
// The caller must pass all authorized grants for this service, not policy paths.
func SecretServiceConfigHash(stack, name string, svc *Service, target string, resolved []ResolvedSecret) (string, error) {
	if len(svc.Secrets) == 0 {
		return ServiceConfigHash(stack, name, svc)
	}
	if !secretIdentifier.MatchString(target) || len(resolved) != len(svc.Secrets) {
		return "", ErrSecretUnavailable
	}
	type reference struct {
		Name, Source, Revision, Target string
		UID, GID, Mode                 int64
	}
	refs := make([]reference, 0, len(resolved))
	grants := map[string]SecretGrant{}
	for _, g := range svc.Secrets {
		if _, ok := grants[g.Name]; ok {
			return "", ErrSecretUnavailable
		}
		grants[g.Name] = g
	}
	for _, r := range resolved {
		g, ok := grants[r.Name]
		if !ok || g.Target != r.Target || g.UID != r.UID || g.GID != r.GID || g.Mode != r.Mode || !secretIdentifier.MatchString(r.Source) || !secretIdentifier.MatchString(r.Revision) {
			return "", ErrSecretUnavailable
		}
		delete(grants, r.Name)
		refs = append(refs, reference{r.Name, r.Source, r.Revision, r.Target, r.UID, r.GID, r.Mode})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	payload, err := serviceHashPayload(stack, name, svc)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(struct {
		Domain  string
		Service json.RawMessage
		Target  string
		Grants  []reference
	}{"dill-secret-hash/1", payload, target, refs})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
