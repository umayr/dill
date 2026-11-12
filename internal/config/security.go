package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const SystemLabelPrefix = "dill."

// ValidateRuntime checks invariants that Pkl's type system cannot express.
// These checks protect Dill's ownership labels and catch shorthand values that
// are parsed after Pkl evaluation.
func ValidateRuntime(cfg *DillConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if err := ValidateSecrets(cfg); err != nil {
		return err
	}
	containerNames := make(map[string]string)
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		svc := cfg.Services[name]
		if svc == nil {
			return fmt.Errorf("service %q is nil", name)
		}
		if svc.ContainerName != "" {
			if previous, exists := containerNames[svc.ContainerName]; exists {
				return fmt.Errorf("service %q containerName %q duplicates service %q", name, svc.ContainerName, previous)
			}
			containerNames[svc.ContainerName] = name
		}
		for label := range svc.Labels {
			if strings.HasPrefix(label, SystemLabelPrefix) {
				return fmt.Errorf("service %q label %q uses reserved prefix %q", name, label, SystemLabelPrefix)
			}
		}
		if _, err := NormalizePorts(svc.Ports); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if _, err := NormalizeVolumes(svc.Volumes, svc.BaseDir); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if _, err := NormalizeResources(svc); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if svc.HealthCheck != nil {
			if svc.HealthCheck.Retries != nil && *svc.HealthCheck.Retries < 0 {
				return fmt.Errorf("service %q healthcheck retries: must be non-negative", name)
			}
			for _, field := range []struct {
				name string
				raw  *json.RawMessage
			}{
				{"interval", svc.HealthCheck.Interval}, {"timeout", svc.HealthCheck.Timeout}, {"startPeriod", svc.HealthCheck.StartPeriod},
			} {
				if _, err := ParsePklDuration(field.raw); err != nil {
					return fmt.Errorf("service %q healthcheck %s: %w", name, field.name, err)
				}
			}
			if _, err := NormalizeHealthCheckTest(svc.HealthCheck.Test); err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
		}
	}
	return ValidateGraph(cfg.Services)
}

// ValidateContainerNames checks names after stack-name overrides are resolved.
func ValidateContainerNames(services map[string]*Service, stack string) error {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	used := make(map[string]string, len(names))
	for _, name := range names {
		svc := services[name]
		if svc == nil {
			return fmt.Errorf("service %q is nil", name)
		}
		container := svc.ContainerName
		if container == "" {
			container = stack + "_" + name
		}
		if previous, exists := used[container]; exists {
			return fmt.Errorf("service %q containerName %q conflicts with service %q", name, container, previous)
		}
		used[container] = name
	}
	return nil
}

// ValidateGraph checks the entire declared dependency graph before target access.
// networkMode remains engine-native; managed namespace edges are deferred to #9.
func ValidateGraph(services map[string]*Service) error {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	state := make(map[string]uint8, len(names))
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 {
			return fmt.Errorf("cycle detected involving service %q dependsOn", name)
		}
		if state[name] == 2 {
			return nil
		}
		svc := services[name]
		if svc == nil {
			return fmt.Errorf("service %q is nil", name)
		}
		state[name] = 1
		for i, dep := range svc.DependsOn {
			if _, ok := services[dep]; !ok {
				return fmt.Errorf("service %q dependsOn[%d]: %q is not defined", name, i, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
