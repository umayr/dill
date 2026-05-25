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
	"github.com/docker/docker/api/types/network"
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

func (d *DockerEngine) PullImage(ctx context.Context, img string, w io.Writer) error {
	logger.Debug("pulling image", "image", img)
	rc, err := d.cli.ImagePull(ctx, img, dockertypes.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", img, err)
	}
	defer rc.Close()

	type layerState struct{ current, total int64 }
	layers := make(map[string]*layerState)

	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Status         string `json:"status"`
			Error          string `json:"error"`
			ID             string `json:"id"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
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
		logger.Debug("pull", "image", img, "status", msg.Status)

		switch msg.Status {
		case "Downloading":
			if msg.ID != "" && msg.ProgressDetail.Total > 0 {
				if layers[msg.ID] == nil {
					layers[msg.ID] = &layerState{}
				}
				layers[msg.ID].current = msg.ProgressDetail.Current
				layers[msg.ID].total = msg.ProgressDetail.Total
				var cur, tot int64
				for _, lp := range layers {
					cur += lp.current
					tot += lp.total
				}
				if tot > 0 {
					fmt.Fprintf(w, "%s / %s\n", formatBytes(cur), formatBytes(tot))
				}
			}
		case "Pull complete", "Already exists":
			if lp := layers[msg.ID]; lp != nil {
				lp.current = lp.total
			}
		}
	}
	return nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
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
	logger.Debug("starting service", "service", name, "image", svc.Image)

	ports, err := config.NormalizePorts(svc.Ports)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}
	volumes, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
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
		Cmd:          strslice.StrSlice(svc.Command),
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

	// Attach to the per-stack user-defined network (enables DNS by service name).
	// Skip if the service uses a special network mode (host, container:X, etc.).
	var netCfg *network.NetworkingConfig
	if svc.NetworkMode == "" {
		netCfg = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				stackName: {Aliases: []string{name}},
			},
		}
	}

	resp, err := d.cli.ContainerCreate(ctx, containerCfg, hostCfg, netCfg, nil, containerName)
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
	logger.Debug("stopping service", "service", name)
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
		// Crash-loop detection: exited non-zero and already restarted at least once.
		if info.State.ExitCode != 0 && info.RestartCount > 0 {
			return false, fmt.Errorf("container %s is crash-looping (exit %d, %d restart(s))",
				name, info.State.ExitCode, info.RestartCount)
		}
		return false, nil
	}
	if hasHealthCheck {
		if info.State.Health == nil {
			return false, nil
		}
		if info.State.Health.Status == "unhealthy" {
			return false, fmt.Errorf("container %s is unhealthy", name)
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

func (d *DockerEngine) InspectConfig(ctx context.Context, name string) (*LiveConfig, error) {
	info, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, err
	}

	// Env: split "KEY=VAL" pairs into a map.
	env := make(map[string]string, len(info.Config.Env))
	for _, e := range info.Config.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}

	// Ports: nat.PortMap → []config.PortBinding.
	var ports []config.PortBinding
	for containerPort, bindings := range info.NetworkSettings.Ports {
		target := containerPort.Port()
		proto := containerPort.Proto()
		if len(bindings) == 0 {
			ports = append(ports, config.PortBinding{Target: target, Protocol: proto})
			continue
		}
		for _, b := range bindings {
			ports = append(ports, config.PortBinding{
				HostIP:    b.HostIP,
				Published: b.HostPort,
				Target:    target,
				Protocol:  proto,
			})
		}
	}

	// Mounts → []config.VolumeMount.
	// For named volumes Docker resolves Source to the host path
	// (/var/lib/docker/volumes/<name>/_data). Use the Name field instead so
	// comparisons against the desired config (which uses the volume name) work.
	var volumes []config.VolumeMount
	for _, m := range info.Mounts {
		source := m.Source
		if m.Type == "volume" && m.Name != "" {
			source = m.Name
		}
		volumes = append(volumes, config.VolumeMount{
			Type:     string(m.Type),
			Source:   source,
			Target:   m.Destination,
			ReadOnly: !m.RW,
		})
	}

	// Labels: strip dill.* system labels.
	userLabels := make(map[string]string)
	for k, v := range info.Config.Labels {
		if !strings.HasPrefix(k, "dill.") {
			userLabels[k] = v
		}
	}

	var healthTest []string
	if info.Config.Healthcheck != nil {
		healthTest = info.Config.Healthcheck.Test
	}

	init := info.HostConfig.Init != nil && *info.HostConfig.Init

	return &LiveConfig{
		Image:         info.Config.Image,
		Env:           env,
		Ports:         ports,
		Volumes:       volumes,
		RestartPolicy: string(info.HostConfig.RestartPolicy.Name),
		UserLabels:    userLabels,
		NetworkMode:   string(info.HostConfig.NetworkMode),
		User:          info.Config.User,
		HealthTest:    healthTest,
		Command:       []string(info.Config.Cmd),
		CapAdd:        []string(info.HostConfig.CapAdd),
		Init:          init,
		Cpuset:        info.HostConfig.Resources.CpusetCpus,
	}, nil
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

func (d *DockerEngine) EnsureNetwork(ctx context.Context, name string) error {
	list, err := d.cli.NetworkList(ctx, dockertypes.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("name", name)),
	})
	if err != nil {
		return fmt.Errorf("listing networks: %w", err)
	}
	for _, n := range list {
		if n.Name == name {
			logger.Debug("network already exists", "name", name)
			return nil
		}
	}
	_, err = d.cli.NetworkCreate(ctx, name, dockertypes.NetworkCreate{
		Driver: "bridge",
		Labels: map[string]string{
			"dill.managed": "true",
			"dill.stack":   name,
		},
	})
	if err != nil {
		return fmt.Errorf("creating network %s: %w", name, err)
	}
	logger.Debug("network created", "name", name)
	return nil
}

func (d *DockerEngine) RemoveNetwork(ctx context.Context, name string) error {
	if err := d.cli.NetworkRemove(ctx, name); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing network %s: %w", name, err)
	}
	logger.Debug("network removed", "name", name)
	return nil
}

func (d *DockerEngine) RemoveImage(ctx context.Context, image string, force bool) error {
	_, err := d.cli.ImageRemove(ctx, image, dockertypes.ImageRemoveOptions{Force: force})
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing image %s: %w", image, err)
	}
	logger.Debug("image removed", "image", image)
	return nil
}

func (d *DockerEngine) RemoveVolume(ctx context.Context, name string, force bool) error {
	if err := d.cli.VolumeRemove(ctx, name, force); err != nil {
		if client.IsErrNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	logger.Debug("volume removed", "name", name)
	return nil
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
	if d, err := config.ParsePklDuration(hc.Interval); err == nil && d > 0 {
		cfg.Interval = d
	}
	if d, err := config.ParsePklDuration(hc.Timeout); err == nil && d > 0 {
		cfg.Timeout = d
	}
	if d, err := config.ParsePklDuration(hc.StartPeriod); err == nil && d > 0 {
		cfg.StartPeriod = d
	}
	return cfg
}
