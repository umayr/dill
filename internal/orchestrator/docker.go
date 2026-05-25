package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

type DockerEngine struct {
	cli *client.Client
}

func NewDockerEngine() (*DockerEngine, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerEngine{cli: cli}, nil
}

func (d *DockerEngine) PullImage(ctx context.Context, img string) error {
	logger.Debug("pulling image", "image", img)
	rc, err := d.cli.ImagePull(ctx, img, dockertypes.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", img, err)
	}
	defer rc.Close()
	// Drain pull output and log each status line.
	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if msg.Error != "" {
			return fmt.Errorf("pull %s: %s", img, msg.Error)
		}
		if msg.Status != "" {
			logger.Debug("pull", "image", img, "status", msg.Status)
		}
	}
	return nil
}

func (d *DockerEngine) ImageExists(ctx context.Context, img string) (bool, error) {
	_, _, err := d.cli.ImageInspectWithRaw(ctx, img)
	if err != nil {
		if client.IsErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *DockerEngine) StartService(ctx context.Context, name string, svc *config.Service, stackName string) (string, error) {
	logger.Info("starting service", "service", name, "image", svc.Image)

	ports, err := config.NormalizePorts(svc.Ports)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}
	volumes, err := config.NormalizeVolumes(svc.Volumes)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}

	// Build port bindings.
	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	for _, p := range ports {
		proto := p.Protocol
		if proto == "" {
			proto = "tcp"
		}
		containerPort := nat.Port(fmt.Sprintf("%s/%s", p.Target, proto))
		exposedPorts[containerPort] = struct{}{}
		if p.Published != "" {
			hostBinding := nat.PortBinding{HostIP: p.HostIP, HostPort: p.Published}
			portBindings[containerPort] = append(portBindings[containerPort], hostBinding)
		}
	}

	// Build mounts.
	mounts := make([]mount.Mount, 0, len(volumes))
	for _, v := range volumes {
		mt := mount.TypeVolume
		switch v.Type {
		case "bind":
			mt = mount.TypeBind
		case "tmpfs":
			mt = mount.TypeTmpfs
		}
		mounts = append(mounts, mount.Mount{
			Type:     mt,
			Source:   v.Source,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		})
	}

	// Build env slice.
	env := make([]string, 0, len(svc.Environment))
	for k, v := range svc.Environment {
		env = append(env, k+"="+v)
	}

	labels := map[string]string{
		"dill.managed": "true",
		"dill.service": name,
		"dill.stack":   stackName,
	}
	for k, v := range svc.Labels {
		labels[k] = v
	}

	containerName := svc.ContainerName
	if containerName == "" {
		containerName = stackName + "_" + name
	}

	containerCfg := &container.Config{
		Image:        svc.Image,
		Env:          env,
		Labels:       labels,
		ExposedPorts: exposedPorts,
	}
	if svc.User != "" {
		containerCfg.User = svc.User
	}
	if svc.HealthCheck != nil {
		containerCfg.Healthcheck = toDockerHealthCheck(svc.HealthCheck)
	}

	hostCfg := &container.HostConfig{
		PortBindings:  portBindings,
		Mounts:        mounts,
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(svc.Restart)},
		CapAdd:        strslice.StrSlice(svc.CapAdd),
		Resources: container.Resources{
			Devices:    toDeviceMappings(svc.Devices),
			CpusetCpus: svc.Cpuset,
		},
	}
	if svc.NetworkMode != "" {
		hostCfg.NetworkMode = container.NetworkMode(svc.NetworkMode)
	}
	if svc.Init != nil {
		hostCfg.Init = svc.Init
	}

	resp, err := d.cli.ContainerCreate(ctx, containerCfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", name, err)
	}
	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start %s: %w", name, err)
	}
	logger.Debug("container started", "service", name, "id", resp.ID[:12])
	return resp.ID, nil
}

func (d *DockerEngine) StopService(ctx context.Context, name string) error {
	logger.Info("stopping service", "service", name)
	timeout := int(10 * time.Second / time.Second)
	return d.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
}

func (d *DockerEngine) RemoveService(ctx context.Context, name string) error {
	logger.Debug("removing container", "name", name)
	return d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

func (d *DockerEngine) IsReady(ctx context.Context, name string, hasHealthCheck bool) (bool, error) {
	info, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		return false, err
	}
	if !info.State.Running {
		return false, nil
	}
	if hasHealthCheck {
		if info.State.Health == nil {
			return false, nil
		}
		return info.State.Health.Status == "healthy", nil
	}
	return true, nil
}

func (d *DockerEngine) ListStack(ctx context.Context, stackName string) ([]string, error) {
	f := filters.NewArgs(
		filters.Arg("label", "dill.managed=true"),
		filters.Arg("label", "dill.stack="+stackName),
	)
	cs, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: f,
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		if len(c.Names) > 0 {
			names = append(names, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	return names, nil
}

func (d *DockerEngine) StartExisting(ctx context.Context, name string) error {
	return d.cli.ContainerStart(ctx, name, container.StartOptions{})
}

func (d *DockerEngine) ServiceStatus(ctx context.Context, name string) (*ContainerStatus, error) {
	f := filters.NewArgs(filters.Arg("name", "^/"+name+"$"))
	list, err := d.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("container %q not found", name)
	}
	c := list[0]
	id := c.ID
	if len(id) > 12 {
		id = id[:12]
	}
	var ports []string
	for _, p := range c.Ports {
		if p.PublicPort != 0 {
			ports = append(ports, fmt.Sprintf("%s:%d->%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type))
		}
	}
	cname := name
	if len(c.Names) > 0 {
		cname = strings.TrimPrefix(c.Names[0], "/")
	}
	return &ContainerStatus{
		Name:   cname,
		ID:     id,
		State:  c.State,
		Status: c.Status,
		Image:  c.Image,
		Ports:  ports,
	}, nil
}

func (d *DockerEngine) Logs(ctx context.Context, name string, follow bool, tail int, w io.Writer) error {
	tailStr := "all"
	if tail > 0 {
		tailStr = strconv.Itoa(tail)
	}
	rc, err := d.cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tailStr,
	})
	if err != nil {
		return fmt.Errorf("logs %s: %w", name, err)
	}
	defer rc.Close()
	_, err = stdcopy.StdCopy(w, w, rc)
	return err
}

func (d *DockerEngine) Close() error { return d.cli.Close() }

func toDeviceMappings(devices []string) []container.DeviceMapping {
	out := make([]container.DeviceMapping, 0, len(devices))
	for _, d := range devices {
		parts := strings.SplitN(d, ":", 3)
		dm := container.DeviceMapping{PathOnHost: parts[0], PathInContainer: parts[0]}
		if len(parts) >= 2 {
			dm.PathInContainer = parts[1]
		}
		if len(parts) == 3 {
			dm.CgroupPermissions = parts[2]
		} else {
			dm.CgroupPermissions = "rwm"
		}
		out = append(out, dm)
	}
	return out
}

func toDockerHealthCheck(hc *config.HealthCheck) *container.HealthConfig {
	test, err := config.NormalizeHealthCheckTest(hc.Test)
	if err != nil || len(test) == 0 {
		return nil
	}
	cfg := &container.HealthConfig{Test: test}
	if hc.Retries != nil {
		cfg.Retries = *hc.Retries
	}
	return cfg
}
