package config

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// validateDecodedShape checks requirements that Go's zero values otherwise erase
// when a module supplies its own JSON instead of amending dill.pkl.
func validateDecodedShape(raw json.RawMessage, cfg *DillConfig) error {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj) // decodeStrict already checked the shape
	required := func(m map[string]json.RawMessage, key, path string) error {
		v, ok := m[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("%s.%s: required value is missing or null", path, key)
		}
		return nil
	}
	if err := required(obj, "services", "config"); err != nil {
		return err
	}
	if _, ok := obj["engine"]; ok && cfg.Engine != "docker" && cfg.Engine != "podman" {
		return fmt.Errorf("config.engine: invalid engine %q", cfg.Engine)
	}
	var services map[string]json.RawMessage
	_ = json.Unmarshal(obj["services"], &services)
	for name, rawService := range services {
		path := fmt.Sprintf("config.services[%q]", name)
		if bytes.Equal(bytes.TrimSpace(rawService), []byte("null")) {
			return fmt.Errorf("%s: service must not be null", path)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(rawService, &fields)
		if err := required(fields, "image", path); err != nil {
			return err
		}
		svc := cfg.Services[name]
		if svc.Image == "" {
			return fmt.Errorf("%s.image: must not be empty", path)
		}
		if _, ok := fields["restart"]; ok && svc.Restart != "no" && svc.Restart != "always" && svc.Restart != "on-failure" && svc.Restart != "unless-stopped" {
			return fmt.Errorf("%s.restart: invalid value %q", path, svc.Restart)
		}
		if _, ok := fields["pullPolicy"]; ok && svc.PullPolicy != "always" && svc.PullPolicy != "missing" && svc.PullPolicy != "never" {
			return fmt.Errorf("%s.pullPolicy: invalid value %q", path, svc.PullPolicy)
		}
		if v, ok := fields["healthcheck"]; ok && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			var hc map[string]json.RawMessage
			_ = json.Unmarshal(v, &hc)
			if err := required(hc, "test", path+".healthcheck"); err != nil {
				return err
			}
		}
	}
	return nil
}
