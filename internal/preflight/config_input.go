package preflight

import (
	"errors"
	"strings"

	"github.com/umayr/dill/internal/config"
)

// ConfigInput binds the ACTUAL evaluated supported config, never a caller's
// source-snapshot assertion. prepared is exhaustive service->verified local ID;
// callers must obtain it from the retained engine's metadata inspector, including
// services whose containers are absent. This helper does not inspect or pull,
// qualify a target, or replace whole-profile/ownership/storage validation.
func ConfigInput(cfg *config.DillConfig, stack, operation string, flags EffectiveFlags, prepared map[string]string) (Input, error) {
	bad := errors.New("complete effective input unavailable")
	if cfg == nil || cfg.Engine != "docker" || len(cfg.Secrets) != 0 || len(prepared) != len(cfg.Services) {
		return Input{}, bad
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		return Input{}, bad
	}
	v := Input{LogicalContract: Version, Engine: cfg.Engine, Stack: stack, Operation: operation, Flags: flags, Services: []ServiceInput{}}
	for name, svc := range cfg.Services {
		if svc == nil || len(svc.Secrets) != 0 || svc.PullPolicy != "never" {
			return Input{}, bad
		}
		image, ok := prepared[name]
		if !ok {
			return Input{}, bad
		}
		image, ok = canonicalPreparedID(image)
		if !ok {
			return Input{}, bad
		}
		// Deliberately do NOT replace svc.Image with image before hashing.
		original, err := config.ServiceConfigHash(stack, name, svc)
		if err != nil {
			return Input{}, bad
		}
		container := svc.ContainerName
		if container == "" {
			container = stack + "_" + name
		}
		v.Services = append(v.Services, ServiceInput{Name: name, ContainerName: container, DesiredConfigHash: original, DependsOn: append([]string{}, svc.DependsOn...), EffectivePullPolicy: "never", PreparedImageID: image})
	}
	v, err := v.normalized()
	if err != nil {
		return Input{}, bad
	}
	return v, nil
}
func canonicalPreparedID(raw string) (string, bool) {
	id := strings.TrimPrefix(raw, "sha256:")
	if !ValidID(id) {
		return "", false
	}
	return "sha256:" + id, true
}
func (v Invocation) EffectiveFlags() EffectiveFlags {
	return EffectiveFlags{TimeoutNanos: int64(v.Timeout), ForceRecreate: v.ForceRecreate, NoPrune: v.NoPrune, EffectivePullPolicy: "never", NoDeps: false, CallerSource: v.Source}
}
