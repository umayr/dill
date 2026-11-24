package orchestrator

import (
	"context"
	"github.com/containers/podman/v5/pkg/bindings/system"
)

func (d *DockerEngine) DiagnosticVersion(ctx context.Context) string {
	v, err := d.cli.ServerVersion(ctx)
	if err != nil {
		return "unknown"
	}
	return v.Version
}
func (p *PodmanEngine) DiagnosticVersion(ctx context.Context) string {
	v, err := system.Version(p.operationContext(ctx), nil)
	if err != nil || v == nil || v.Server == nil {
		return "unknown"
	}
	return v.Server.Version
}
