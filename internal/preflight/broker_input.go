package preflight

import (
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
)

// BrokerConfigInput retains original desired provenance and verified immutable
// image IDs, but hashes only authenticated broker generation references.
func BrokerConfigInput(cfg *config.DillConfig, stack string, flags EffectiveFlags, prepared map[string]string, m *brokersecret.Materialized) (Input, error) {
	if cfg == nil || (cfg.Engine != "docker" && cfg.Engine != "podman") || cfg.Engine != mEngine(m) || m == nil || stack != m.Scope().Stack || len(prepared) != len(cfg.Services) {
		return Input{}, brokersecret.ErrUnavailable
	}
	refs, target, err := m.References(cfg, false)
	if err != nil {
		return Input{}, err
	}
	v := Input{LogicalContract: Version, Engine: cfg.Engine, Stack: stack, Operation: "up", Flags: flags, Services: []ServiceInput{}}
	for name, svc := range cfg.Services {
		if svc == nil || svc.PullPolicy != "never" {
			return Input{}, brokersecret.ErrUnavailable
		}
		image, ok := canonicalPreparedID(prepared[name])
		if !ok {
			return Input{}, brokersecret.ErrUnavailable
		}
		hash, err := config.SecretServiceConfigHash(stack, name, svc, target, refs[name])
		if err != nil {
			return Input{}, brokersecret.ErrUnavailable
		}
		container := svc.ContainerName
		if container == "" {
			container = stack + "_" + name
		}
		v.Services = append(v.Services, ServiceInput{Name: name, ContainerName: container, DesiredConfigHash: hash, DependsOn: append([]string(nil), svc.DependsOn...), EffectivePullPolicy: "never", PreparedImageID: image})
	}
	normalized, err := v.normalizedMode(true)
	if err != nil {
		return Input{}, brokersecret.ErrUnavailable
	}
	return normalized, nil
}

func mEngine(m *brokersecret.Materialized) string {
	if m == nil {
		return ""
	}
	return m.Scope().Engine
}

// BrokerSelection is a new outer domain. A digest from ordinary named review
// cannot authorize a broker operation, or vice versa. The inner selection must
// still contain exhaustive ownership/resource facts from the retained lease.
type BrokerSelection struct {
	Contract string             `json:"contract"`
	Scope    brokersecret.Scope `json:"scope"`
	Handle   string             `json:"handle"`
	Named    Selection          `json:"named"`
}

func (s BrokerSelection) Digest() (string, error) {
	if s.Contract != brokersecret.Version || !s.Scope.Valid() || !brokersecret.ValidHandle(s.Handle) || s.Named.Operation != "up" || s.Named.Stack != s.Scope.Stack || s.Named.Target.Engine != s.Scope.Engine || s.Named.Target.EndpointDigest != s.Scope.EndpointDigest || s.Named.Target.QualifiedIdentityDigest != s.Scope.TargetDigest {
		return "", brokersecret.ErrUnavailable
	}
	named, err := s.Named.digestMode(true)
	if err != nil {
		return "", brokersecret.ErrUnavailable
	}
	return digest("dill.broker-selection/1", struct {
		Scope         brokersecret.Scope
		Handle, Named string
	}{s.Scope, s.Handle, named})
}
