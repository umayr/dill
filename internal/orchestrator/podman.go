package orchestrator

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	nettypes "github.com/containers/common/libnetwork/types"
	"github.com/containers/image/v5/manifest"
	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	podmannetwork "github.com/containers/podman/v5/pkg/bindings/network"
	podmanvolumes "github.com/containers/podman/v5/pkg/bindings/volumes"
	"github.com/containers/podman/v5/pkg/specgen"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

type PodmanEngine struct {
	conn context.Context
}

func NewPodmanEngine(ctx context.Context, socket string) (*PodmanEngine, error) {
	conn, err := bindings.NewConnection(ctx, socket)
	if err != nil {
		return nil, err
	}
	return &PodmanEngine{conn: conn}, nil
}

func (p *PodmanEngine) PullImage(ctx context.Context, img string, w io.Writer) error {
	logger.Debug("pulling image", "image", img)
	_, err := images.Pull(p.conn, img, nil)
	if err != nil {
		return fmt.Errorf("pull %s: %w", img, err)
	}
	return nil
}

func (p *PodmanEngine) ImageExists(ctx context.Context, img string) (bool, error) {
	exists, err := images.Exists(p.conn, img, nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (p *PodmanEngine) StartService(ctx context.Context, name string, svc *config.Service, stackName string) (string, error) {
	logger.Debug("starting service", "service", name, "image", svc.Image)

	ports, err := config.NormalizePorts(svc.Ports)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}
	volumes, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}

	s := specgen.NewSpecGenerator(svc.Image, false)

	containerName := svc.ContainerName
	if containerName == "" {
		containerName = stackName + "_" + name
	}
	s.Name = containerName

	s.Labels = map[string]string{
		"dill.managed":     "true",
		"dill.service":     name,
		"dill.stack":       stackName,
		"dill.config-hash": "",
	}
	if hash, err := config.ServiceConfigHash(name, svc); err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	} else {
		s.Labels["dill.config-hash"] = hash
	}
	for k, v := range svc.Labels {
		s.Labels[k] = v
	}

	s.Env = svc.Environment

	for _, pb := range ports {
		pm, err := toSpecPortMapping(pb)
		if err != nil {
			return "", fmt.Errorf("service %s: %w", name, err)
		}
		s.PortMappings = append(s.PortMappings, pm)
	}

	for _, vm := range volumes {
		s.Mounts = append(s.Mounts, specs.Mount{
			Type:        vm.Type,
			Source:      vm.Source,
			Destination: vm.Target,
			Options:     volumeOptions(vm),
		})
	}

	if len(svc.Command) > 0 {
		s.Command = svc.Command
	}
	if svc.Restart != "" && svc.Restart != "no" {
		s.RestartPolicy = svc.Restart
	}
	if svc.User != "" {
		s.User = svc.User
	}
	if svc.NetworkMode != "" {
		s.NetNS = specgen.Namespace{NSMode: specgen.NamespaceMode(svc.NetworkMode)}
	} else {
		s.Networks = map[string]nettypes.PerNetworkOptions{
			stackName: {Aliases: []string{name}},
		}
	}
	if svc.Init != nil {
		s.Init = svc.Init
	}
	s.CapAdd = svc.CapAdd
	if len(svc.Devices) > 0 {
		logger.Warn("device mapping in Podman requires host device lookup; devices will be passed as-is",
			"service", name)
		for _, d := range svc.Devices {
			parts := strings.SplitN(d, ":", 2)
			dev := specs.LinuxDevice{Path: parts[0]}
			if len(parts) == 2 {
				dev.Path = parts[1]
			}
			s.Devices = append(s.Devices, dev)
		}
	}
	if svc.Cpuset != "" {
		s.ResourceLimits = &specs.LinuxResources{
			CPU: &specs.LinuxCPU{Cpus: svc.Cpuset},
		}
	}
	if svc.HealthCheck != nil {
		test, err := config.NormalizeHealthCheckTest(svc.HealthCheck.Test)
		if err == nil && len(test) > 0 {
			s.HealthConfig = &manifest.Schema2HealthConfig{Test: test}
			if svc.HealthCheck.Retries != nil {
				s.HealthConfig.Retries = *svc.HealthCheck.Retries
			}
			if d, err := config.ParsePklDuration(svc.HealthCheck.Interval); err == nil && d > 0 {
				s.HealthConfig.Interval = d
			}
			if d, err := config.ParsePklDuration(svc.HealthCheck.Timeout); err == nil && d > 0 {
				s.HealthConfig.Timeout = d
			}
			if d, err := config.ParsePklDuration(svc.HealthCheck.StartPeriod); err == nil && d > 0 {
				s.HealthConfig.StartPeriod = d
			}
		}
	}

	res, err := containers.CreateWithSpec(p.conn, s, nil)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", name, err)
	}
	if err := containers.Start(p.conn, res.ID, nil); err != nil {
		if isPortConflict(err) {
			return "", fmt.Errorf("port conflict: another process is bound to one of the requested host ports (%w)", err)
		}
		return "", fmt.Errorf("start %s: %w", name, err)
	}
	logger.Debug("container started", "service", name, "id", res.ID[:12])
	return res.ID, nil
}

func (p *PodmanEngine) StopService(ctx context.Context, name string) error {
	logger.Debug("stopping service", "service", name)
	timeout := uint(10)
	return containers.Stop(p.conn, name, &containers.StopOptions{Timeout: &timeout})
}

func (p *PodmanEngine) RemoveService(ctx context.Context, name string) error {
	logger.Debug("removing container", "name", name)
	force := true
	_, err := containers.Remove(p.conn, name, &containers.RemoveOptions{Force: &force})
	return err
}

func (p *PodmanEngine) IsReady(ctx context.Context, name string, hasHealthCheck bool) (bool, error) {
	data, err := containers.Inspect(p.conn, name, nil)
	if err != nil {
		return false, err
	}
	if data.State == nil || !data.State.Running {
		if data.State != nil && data.State.ExitCode != 0 && data.RestartCount > 0 {
			return false, fmt.Errorf("container %s is crash-looping (exit %d, %d restart(s))",
				name, data.State.ExitCode, data.RestartCount)
		}
		return false, nil
	}
	if hasHealthCheck {
		if data.State.Health == nil {
			return false, nil
		}
		if data.State.Health.Status == "unhealthy" {
			return false, fmt.Errorf("container %s is unhealthy", name)
		}
		return data.State.Health.Status == "healthy", nil
	}
	return true, nil
}

func (p *PodmanEngine) ListStack(ctx context.Context, stackName string) ([]string, error) {
	filter := map[string][]string{
		"label": {
			"dill.managed=true",
			"dill.stack=" + stackName,
		},
	}
	all := true
	list, err := containers.List(p.conn, &containers.ListOptions{
		All:     &all,
		Filters: filter,
	})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		if len(c.Names) > 0 {
			names = append(names, strings.TrimPrefix(c.Names[0], "/"))
		}
	}
	return names, nil
}

func (p *PodmanEngine) StartExisting(ctx context.Context, name string) error {
	err := containers.Start(p.conn, name, nil)
	if err != nil && strings.Contains(err.Error(), "no such container") {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return err
}

func (p *PodmanEngine) ServiceStatus(ctx context.Context, name string) (*ContainerStatus, error) {
	data, err := containers.Inspect(p.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such container") {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, err
	}
	id := data.ID
	if len(id) > 12 {
		id = id[:12]
	}
	state := ""
	if data.State != nil {
		state = data.State.Status
	}
	var ports []string
	if data.NetworkSettings != nil {
		for portProto, binds := range data.NetworkSettings.Ports {
			for _, b := range binds {
				ports = append(ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, portProto))
			}
		}
	}
	return &ContainerStatus{
		Name:   strings.TrimPrefix(data.Name, "/"),
		ID:     id,
		State:  state,
		Status: state,
		Image:  data.ImageName,
		Ports:  ports,
	}, nil
}

func (p *PodmanEngine) Logs(ctx context.Context, name string, follow bool, tail int, w io.Writer) error {
	tailStr := "all"
	if tail > 0 {
		tailStr = strconv.Itoa(tail)
	}
	opts := new(containers.LogOptions).
		WithFollow(follow).
		WithTail(tailStr).
		WithStdout(true).
		WithStderr(true)

	stdoutCh := make(chan string, 64)
	stderrCh := make(chan string, 64)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for line := range stdoutCh {
			fmt.Fprint(w, line)
		}
	}()
	go func() {
		defer wg.Done()
		for line := range stderrCh {
			fmt.Fprint(w, line)
		}
	}()

	err := containers.Logs(p.conn, name, opts, stdoutCh, stderrCh)
	close(stdoutCh)
	close(stderrCh)
	wg.Wait()
	return err
}

func (p *PodmanEngine) InspectConfig(ctx context.Context, name string) (*LiveConfig, error) {
	data, err := containers.Inspect(p.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such container") {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, err
	}

	// Env: split "KEY=VAL" pairs into a map.
	env := make(map[string]string)
	if data.Config != nil {
		for _, e := range data.Config.Env {
			if k, v, ok := strings.Cut(e, "="); ok {
				env[k] = v
			}
		}
	}

	// Ports: map["port/proto"][]InspectHostPort → []config.PortBinding.
	var ports []config.PortBinding
	if data.NetworkSettings != nil {
		for portProto, binds := range data.NetworkSettings.Ports {
			target, proto, _ := strings.Cut(portProto, "/")
			if len(binds) == 0 {
				ports = append(ports, config.PortBinding{Target: target, Protocol: proto})
				continue
			}
			for _, b := range binds {
				ports = append(ports, config.PortBinding{
					HostIP:    b.HostIP,
					Published: b.HostPort,
					Target:    target,
					Protocol:  proto,
				})
			}
		}
	}

	// Mounts → []config.VolumeMount.
	// Same normalisation as Docker: use Name for named volumes so comparisons
	// against the desired config (volume name) work correctly.
	var volumes []config.VolumeMount
	for _, m := range data.Mounts {
		source := m.Source
		if m.Type == "volume" && m.Name != "" {
			source = m.Name
		}
		volumes = append(volumes, config.VolumeMount{
			Type:     m.Type,
			Source:   source,
			Target:   m.Destination,
			ReadOnly: !m.RW,
		})
	}

	// Labels: strip dill.* system labels.
	userLabels := make(map[string]string)
	systemLabels := make(map[string]string)
	if data.Config != nil {
		for k, v := range data.Config.Labels {
			if strings.HasPrefix(k, "dill.") {
				systemLabels[k] = v
			} else {
				userLabels[k] = v
			}
		}
	}

	var healthTest []string
	if data.Config != nil && data.Config.Healthcheck != nil {
		healthTest = data.Config.Healthcheck.Test
	}

	restartPolicy := ""
	networkMode := ""
	capAdd := []string(nil)
	cpuset := ""
	init := false
	if data.HostConfig != nil {
		restartPolicy = data.HostConfig.RestartPolicy.Name
		networkMode = data.HostConfig.NetworkMode
		capAdd = data.HostConfig.CapAdd
		cpuset = data.HostConfig.CpusetCpus
		init = data.HostConfig.Init
	}
	user := ""
	var command []string
	if data.Config != nil {
		user = data.Config.User
		command = data.Config.Cmd
	}

	return &LiveConfig{
		Image:         data.ImageName,
		Env:           env,
		Ports:         ports,
		Volumes:       volumes,
		RestartPolicy: restartPolicy,
		UserLabels:    userLabels,
		SystemLabels:  systemLabels,
		NetworkMode:   networkMode,
		User:          user,
		HealthTest:    healthTest,
		Command:       command,
		CapAdd:        capAdd,
		Init:          init,
		Cpuset:        cpuset,
	}, nil
}

func (p *PodmanEngine) EnsureNetwork(ctx context.Context, name string) error {
	exists, err := podmannetwork.Exists(p.conn, name, nil)
	if err != nil {
		return fmt.Errorf("checking network: %w", err)
	}
	if exists {
		logger.Debug("network already exists", "name", name)
		return nil
	}
	_, err = podmannetwork.Create(p.conn, &nettypes.Network{
		Name:   name,
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

func (p *PodmanEngine) RemoveNetwork(ctx context.Context, name string) error {
	_, err := podmannetwork.Remove(p.conn, name, nil)
	if err != nil {
		if strings.Contains(err.Error(), "no such network") {
			return nil
		}
		return fmt.Errorf("removing network %s: %w", name, err)
	}
	logger.Debug("network removed", "name", name)
	return nil
}

func (p *PodmanEngine) RemoveImage(ctx context.Context, image string, force bool) error {
	opts := new(images.RemoveOptions).WithForce(force)
	_, errs := images.Remove(p.conn, []string{image}, opts)
	for _, err := range errs {
		if err != nil && !strings.Contains(err.Error(), "image not known") {
			return fmt.Errorf("removing image %s: %w", image, err)
		}
	}
	logger.Debug("image removed", "image", image)
	return nil
}

func (p *PodmanEngine) RemoveVolume(ctx context.Context, name string, force bool) error {
	opts := new(podmanvolumes.RemoveOptions).WithForce(force)
	if err := podmanvolumes.Remove(p.conn, name, opts); err != nil {
		if strings.Contains(err.Error(), "no such volume") {
			return nil
		}
		return fmt.Errorf("removing volume %s: %w", name, err)
	}
	logger.Debug("volume removed", "name", name)
	return nil
}

func (p *PodmanEngine) Close() error { return nil }

func toSpecPortMapping(pb config.PortBinding) (nettypes.PortMapping, error) {
	var containerPort uint16
	if _, err := fmt.Sscan(pb.Target, &containerPort); err != nil {
		return nettypes.PortMapping{}, fmt.Errorf("invalid container port %q", pb.Target)
	}
	pm := nettypes.PortMapping{
		ContainerPort: containerPort,
		HostIP:        pb.HostIP,
		Protocol:      pb.Protocol,
	}
	if pb.Published != "" {
		var hostPort uint16
		if _, err := fmt.Sscan(pb.Published, &hostPort); err != nil {
			return nettypes.PortMapping{}, fmt.Errorf("invalid host port %q", pb.Published)
		}
		pm.HostPort = hostPort
	}
	return pm, nil
}

func volumeOptions(vm config.VolumeMount) []string {
	if vm.ReadOnly {
		return []string{"ro"}
	}
	return nil
}
