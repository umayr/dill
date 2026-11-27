package main

import (
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"strings"
)

func (o brokerCLIOptions) nativeProfiles() []*coordination.BrokerProfile {
	if o.profile == nil {
		return nil
	}
	return []*coordination.BrokerProfile{o.profile}
}
func brokerProfileOwners(p *coordination.BrokerProfile, grants []brokersecret.Grant) bool {
	var n *coordination.NamespaceProfile
	if p.Podman != nil {
		n = &p.Podman.Namespace
	} else {
		n = p.Namespace
	}
	if n == nil {
		return true
	}
	for _, g := range grants {
		if g.UID < 0 || g.GID < 0 || g.UID > 2147483647 || g.GID > 2147483647 || !n.Owners(uint32(g.UID), uint32(g.GID)) {
			return false
		}
	}
	return true
}

func scopedBrokerProfile(m *brokersecret.Materialized, profiles []*coordination.BrokerProfile) (*coordination.BrokerProfile, error) {
	if len(profiles) == 0 {
		if m != nil && m.Scope().Engine == "podman" {
			return nil, brokersecret.ErrUnavailable
		}
		return nil, nil
	}
	if len(profiles) != 1 || profiles[0] == nil || m == nil {
		return nil, brokersecret.ErrUnavailable
	}
	raw, err := json.Marshal(profiles[0])
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	var p coordination.BrokerProfile
	if json.Unmarshal(raw, &p) != nil || !p.Valid() {
		return nil, brokersecret.ErrUnavailable
	}
	digest, err := p.TargetDigest()
	if err != nil || digest != m.Scope().TargetDigest || p.Engine() != m.Scope().Engine {
		return nil, brokersecret.ErrUnavailable
	}
	return &p, nil
}
func brokerProfileOptions(p *coordination.BrokerProfile, opts commandOptions, layout, daemon string) bool {
	if p == nil || !p.Valid() || p.Engine() != opts.Engine || p.Socket() != strings.TrimPrefix(opts.EngineSocket, "unix://") {
		return false
	}
	if p.Docker != nil {
		return p.Docker.Layout == layout && p.Docker.DaemonID == daemon
	}
	return p.Podman.Layout == layout && daemon == ""
}
