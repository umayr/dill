package orchestrator

import (
	"context"
	"fmt"
	pv "github.com/containers/podman/v5/pkg/bindings/volumes"
	pt "github.com/containers/podman/v5/pkg/domain/entities/types"
	volume "github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/umayr/dill/internal/config"
	"maps"
	"strings"
)

type declarationAPI interface {
	inspectDeclaration(context.Context, string) (*volumeEvidence, error)
	createDeclaration(context.Context, string, string, config.VolumeDeclaration) error
}
type volumeEvidence struct {
	name, driver string
	opts, labels map[string]string
}

func declarationLabels(stack string, d config.VolumeDeclaration) map[string]string {
	l := map[string]string{"dill.managed": "true", "dill.stack": stack}
	maps.Copy(l, d.Labels)
	return l
}
func declared(ctx context.Context, e declarationAPI, name, stack string, d config.VolumeDeclaration, create bool) (string, error) {
	if d.Ownership != config.VolumeManaged && d.Ownership != config.VolumeExternal {
		return "", fmt.Errorf("invalid volume ownership")
	}
	for key := range d.Labels {
		if strings.HasPrefix(key, "dill.") {
			return "", fmt.Errorf("reserved volume label")
		}
	}
	if name == "" || stack == "" {
		return "", fmt.Errorf("invalid declared volume scope")
	}
	v, err := e.inspectDeclaration(ctx, name)
	if err != nil {
		return "", fmt.Errorf("cannot inspect volume %q", name)
	}
	if v == nil {
		if d.Ownership == config.VolumeExternal {
			return "", fmt.Errorf("external volume %q is missing", name)
		}
		if !create {
			return "create managed", nil
		}
		if err := e.createDeclaration(ctx, name, stack, d); err != nil {
			return "", fmt.Errorf("cannot create managed volume %q", name)
		}
		v, err = e.inspectDeclaration(ctx, name)
		if err != nil || v == nil {
			return "", fmt.Errorf("cannot verify created volume %q", name)
		}
	}
	if v.name != name {
		return "", fmt.Errorf("volume identity mismatch")
	}
	if d.Ownership == config.VolumeExternal {
		return "reuse external", nil
	}
	driver := d.Driver
	if driver == "" {
		driver = "local"
	}
	if v.driver != driver || !maps.Equal(v.opts, d.DriverOptions) {
		return "", fmt.Errorf("volume %q settings differ; retained", name)
	}
	for k, x := range declarationLabels(stack, d) {
		if y, ok := v.labels[k]; !ok || x != y {
			return "", fmt.Errorf("volume %q ownership or labels differ; retained", name)
		}
	}
	return "reuse managed", nil
}
func (d *DockerEngine) inspectDeclaration(ctx context.Context, name string) (*volumeEvidence, error) {
	v, err := d.cli.VolumeInspect(ctx, name)
	if client.IsErrNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &volumeEvidence{v.Name, v.Driver, v.Options, v.Labels}, nil
}
func (d *DockerEngine) createDeclaration(ctx context.Context, name, stack string, v config.VolumeDeclaration) error {
	_, err := d.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Driver: v.Driver, DriverOpts: v.DriverOptions, Labels: declarationLabels(stack, v)})
	return err
}
func (p *PodmanEngine) inspectDeclaration(ctx context.Context, name string) (*volumeEvidence, error) {
	v, err := pv.Inspect(p.operationContext(ctx), name, nil)
	if podmanNotFound(err) {
		return nil, nil
	}
	if err != nil || v == nil {
		return nil, fmt.Errorf("inspect unavailable")
	}
	return &volumeEvidence{v.Name, v.Driver, v.Options, v.Labels}, nil
}
func (p *PodmanEngine) createDeclaration(ctx context.Context, name, stack string, v config.VolumeDeclaration) error {
	_, err := pv.Create(p.operationContext(ctx), pt.VolumeCreateOptions{Name: name, Driver: v.Driver, Options: v.DriverOptions, Labels: declarationLabels(stack, v)}, nil)
	return err
}
func (d *DockerEngine) PlanVolume(ctx context.Context, name, stack string, v config.VolumeDeclaration) (string, error) {
	return declared(ctx, d, name, stack, v, false)
}
func (p *PodmanEngine) PlanVolume(ctx context.Context, name, stack string, v config.VolumeDeclaration) (string, error) {
	return declared(ctx, p, name, stack, v, false)
}
