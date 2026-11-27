package orchestrator

import (
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
)

func checkBrokerRuntime(target *coordination.Target, m *brokersecret.Materialized) error {
	if target == nil || m == nil || "sha256:"+target.Identity() != m.Scope().TargetDigest || target.EndpointDigest() != m.Scope().EndpointDigest || target.Engine() != m.Scope().Engine {
		return brokersecret.ErrUnavailable
	}
	// Existing explicitly qualified rootful Docker retains its original layout.
	// New namespace and Podman profiles require kernel-backed shared-path proof.
	if target.Engine() == "docker" && target.BrokerRootful() && target.NamespaceDigest() == "" {
		return nil
	}
	if target.NamespaceDigest() == "" {
		return brokersecret.ErrUnavailable
	}
	return m.CheckRuntime(func(root string, mounts []brokersecret.Mount) error {
		files := make([]coordination.RuntimeFile, 0, len(mounts))
		for _, mount := range mounts {
			files = append(files, coordination.RuntimeFile{Path: mount.HostPath, UID: mount.UID, GID: mount.GID, Mode: mount.Mode})
		}
		return target.CheckRuntime(root, files)
	})
}
func (d *DockerEngine) VerifyBrokerRuntime(m *brokersecret.Materialized) error {
	if d == nil {
		return brokersecret.ErrUnavailable
	}
	return checkBrokerRuntime(d.brokerTarget, m)
}
func (p *PodmanEngine) VerifyBrokerRuntime(m *brokersecret.Materialized) error {
	if p == nil || p.qualifiedBroker == nil {
		return brokersecret.ErrUnavailable
	}
	return checkBrokerRuntime(p.qualifiedBroker.Target, m)
}
