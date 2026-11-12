package config

import (
	"path"
	"strings"
)

// ValidateSecrets validates authoring without accessing policy or source files.
func ValidateSecrets(cfg *DillConfig) error {
	if cfg == nil {
		return ErrSecretUnavailable
	}
	declared := map[string]bool{}
	for _, req := range cfg.Secrets {
		if !secretIdentifier.MatchString(req.Name) || declared[req.Name] {
			return ErrSecretUnavailable
		}
		declared[req.Name] = true
	}
	for _, svc := range cfg.Services {
		if svc == nil {
			return ErrSecretUnavailable
		}
		names := map[string]bool{}
		targets := []string{}
		mounts, err := NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			return err
		}
		if len(svc.Secrets) > 0 && !secretIdentifier.MatchString(cfg.Name) {
			return ErrSecretUnavailable
		}
		for _, grant := range svc.Secrets {
			if !declared[grant.Name] || names[grant.Name] || !validSecretTarget(grant.Target) || grant.UID < 0 || grant.UID > 2147483647 || grant.GID < 0 || grant.GID > 2147483647 || (grant.Mode != 256 && grant.Mode != 288) {
				return ErrSecretUnavailable
			}
			names[grant.Name] = true
			for _, t := range targets {
				if pathsOverlap(t, grant.Target) {
					return ErrSecretUnavailable
				}
			}
			for _, m := range mounts {
				if pathsOverlap(path.Clean(m.Target), grant.Target) {
					return ErrSecretUnavailable
				}
			}
			targets = append(targets, grant.Target)
		}
	}
	return nil
}
func validSecretTarget(s string) bool {
	return strings.HasPrefix(s, "/run/secrets/") && path.Clean(s) == s && !strings.ContainsRune(s, '\x00')
}
func pathsOverlap(a, b string) bool {
	return a == b || a == "/" || b == "/" || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// RejectUnsupportedSecretDelivery prevents partial builds from silently starting
// workloads without their requested files. Removed when runtime delivery lands.
func RejectUnsupportedSecretDelivery(cfg *DillConfig) error {
	if cfg == nil {
		return ErrSecretUnavailable
	}
	for _, svc := range cfg.Services {
		if svc == nil || len(svc.Secrets) > 0 {
			return ErrSecretUnavailable
		}
	}
	return nil
}
