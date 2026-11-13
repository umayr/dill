package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	nettypes "github.com/containers/common/libnetwork/types"
	"github.com/containers/image/v5/manifest"
	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/bindings/images"
	podmannetwork "github.com/containers/podman/v5/pkg/bindings/network"
	podmanvolumes "github.com/containers/podman/v5/pkg/bindings/volumes"
	"github.com/containers/podman/v5/pkg/domain/entities/reports"
	entitiesTypes "github.com/containers/podman/v5/pkg/domain/entities/types"
	"github.com/containers/podman/v5/pkg/errorhandling"
	"github.com/containers/podman/v5/pkg/specgen"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/secretfile"
)

func podmanNotFound(err error) bool {
	var apiErr *errorhandling.ErrorModel
	return errors.As(err, &apiErr) && apiErr.Code() == http.StatusNotFound
}

type PodmanEngine struct {
	conn       context.Context
	socket     string
	disposable bool
}

func (p *PodmanEngine) EngineName() string { return "podman" }

// EnforceDisposableVolumes is configured before concurrent deployment begins.
func (p *PodmanEngine) EnforceDisposableVolumes() error { p.disposable = true; return nil }

func NewPodmanEngine(ctx context.Context, socket string) (*PodmanEngine, error) {
	conn, err := bindings.NewConnection(ctx, socket)
	if err != nil {
		return nil, err
	}
	// Keep connection values, but do not tie later cleanup to this context's
	// lifetime. Each request gets cancellation and deadlines from its caller.
	return &PodmanEngine{conn: context.WithoutCancel(conn), socket: socket}, nil
}

// podmanOperationContext supplies SDK connection values without replacing the
// operation's deadline, cancellation, or request-scoped values.
type podmanOperationContext struct {
	context.Context
	connection context.Context
}

func (c podmanOperationContext) Value(key any) any {
	if value := c.Context.Value(key); value != nil {
		return value
	}
	return c.connection.Value(key)
}

func (p *PodmanEngine) operationContext(ctx context.Context) context.Context {
	return podmanOperationContext{Context: ctx, connection: p.conn}
}

func (p *PodmanEngine) PullImage(ctx context.Context, img string, w io.Writer) error {
	if p.disposable {
		return ErrDisposableUnsupported
	}
	logger.Debug("pulling image", "image", img)
	_, err := images.Pull(p.operationContext(ctx), img, nil)
	// The SDK can end a cancelled pull stream without returning an error.
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("pull %s: %w", img, err)
	}
	return nil
}

func (p *PodmanEngine) ImageExists(ctx context.Context, img string) (bool, error) {
	exists, err := images.Exists(p.operationContext(ctx), img, nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// LocalImageID returns the local image ID, or "" when the image is absent.
func (p *PodmanEngine) LocalImageID(ctx context.Context, img string) (string, error) {
	data, err := images.GetImage(p.operationContext(ctx), img, nil)
	if podmanNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if data == nil || data.ImageData == nil || data.ID == "" {
		return "", fmt.Errorf("inspect image %q returned an empty ID; image may be retained", img)
	}
	return data.ID, nil
}

// DisposableImage rejects missing metadata instead of assuming no implicit volumes.
func (p *PodmanEngine) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	data, err := images.GetImage(p.operationContext(ctx), image, nil)
	if err != nil || data == nil || data.ImageData == nil || data.ID == "" || data.Config == nil {
		return "", nil, ErrDisposableUnavailable
	}
	volumes := make([]string, 0, len(data.Config.Volumes))
	for target := range data.Config.Volumes {
		volumes = append(volumes, target)
	}
	sort.Strings(volumes)
	return data.ID, volumes, nil
}

func (p *PodmanEngine) StartService(ctx context.Context, name string, svc *config.Service, stackName string) (string, error) {
	if svc == nil || len(svc.Secrets) > 0 {
		return "", config.ErrSecretUnavailable
	}
	return p.startService(ctx, name, svc, stackName, nil, "")
}

func (p *PodmanEngine) StartServiceChecked(ctx context.Context, name string, svc *config.Service, stackName string, req secretfile.Request) (id string, retErr error) {
	defer func() {
		if retErr != nil {
			retErr = config.ErrSecretUnavailable
		}
	}()
	mounts, err := checkedSecretMounts(ctx, name, svc, stackName, "podman", p.socket, req)
	if err != nil {
		return "", config.ErrSecretUnavailable
	}
	return p.startService(ctx, name, svc, stackName, mounts, req.Target)
}

func (p *PodmanEngine) startService(ctx context.Context, name string, svc *config.Service, stackName string, secrets []config.ResolvedSecret, target string) (string, error) {
	image := svc.Image
	if p.disposable {
		if err := ValidateDisposableConfig(&config.DillConfig{Services: map[string]*config.Service{name: svc}}); err != nil {
			return "", err
		}
		id, volumes, err := p.DisposableImage(ctx, image)
		if err != nil {
			return "", err
		}
		if len(volumes) > 0 {
			return "", ErrDisposableUnsupported
		}
		image = id // pin metadata-checked immutable content, never a moving tag
	}
	for key := range svc.Labels {
		if strings.HasPrefix(key, config.SystemLabelPrefix) {
			return "", config.ErrSecretUnavailable
		}
	}

	if err := ValidateCapabilities("podman", map[string]*config.Service{name: svc}); err != nil {
		return "", err
	}
	logger.Debug("starting service", "service", name, "image", svc.Image)

	ports, err := config.NormalizePorts(svc.Ports)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}
	volumes, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}
	resources, err := config.NormalizeResources(svc)
	if err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	}

	s := specgen.NewSpecGenerator(image, false)

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
	if hash, err := config.SecretServiceConfigHash(stackName, name, svc, target, secrets); err != nil {
		return "", fmt.Errorf("service %s: %w", name, err)
	} else {
		s.Labels["dill.config-hash"] = hash
	}
	if len(secrets) > 0 {
		s.Labels[SecretEnabledLabel] = "true"
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

	// Named volumes, bind mounts and tmpfs are modeled differently by the
	// Podman API. Named volumes must go through specgen.Volumes so Podman
	// registers them (auto-creating the volume if missing) and mounts the
	// real volume path; the raw Mounts list is reserved for host paths.
	// Previously named volumes were added to Mounts, which made crun treat
	// the volume name as a host path and fail with "mount ... No such device".
	for _, vm := range volumes {
		switch vm.Type {
		case "volume":
			name := vm.EngineName(stackName)
			if !vm.External && name != "" {
				if err := p.EnsureVolume(ctx, name, stackName); err != nil {
					return "", fmt.Errorf("service %s: %w", name, err)
				}
			}
			s.Volumes = append(s.Volumes, &specgen.NamedVolume{
				Name:    name,
				Dest:    vm.Target,
				Options: volumeOptions(vm),
			})
		default:
			s.Mounts = append(s.Mounts, specs.Mount{
				Type:        vm.Type,
				Source:      vm.Source,
				Destination: vm.Target,
				Options:     volumeOptions(vm),
			})
		}
	}

	for _, secret := range secrets {
		s.Mounts = append(s.Mounts, specs.Mount{Type: "bind", Source: secret.HostPath, Destination: secret.Target, Options: []string{"ro"}})
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
		s.NetNS = specgen.Namespace{NSMode: specgen.Bridge}
		s.Networks = map[string]nettypes.PerNetworkOptions{
			stackName: {Aliases: []string{name}},
		}
	}
	if svc.Init != nil {
		s.Init = svc.Init
	}
	s.CapAdd = svc.CapAdd
	linuxRes := &specs.LinuxResources{}
	hasResources := false
	if resources.Memory != 0 || resources.MemoryReservation != 0 {
		mem := &specs.LinuxMemory{}
		if resources.Memory != 0 {
			limit := resources.Memory
			mem.Limit = &limit
		}
		if resources.MemoryReservation != 0 {
			reservation := resources.MemoryReservation
			mem.Reservation = &reservation
		}
		linuxRes.Memory = mem
		hasResources = true
	}
	if resources.CPUShares != 0 || resources.NanoCPUs != 0 || svc.Cpuset != "" {
		cpu := &specs.LinuxCPU{}
		if resources.CPUShares != 0 {
			shares := uint64(resources.CPUShares)
			cpu.Shares = &shares
		}
		if resources.NanoCPUs != 0 {
			period := uint64(100000)
			quota := resources.NanoCPUs / 10000
			cpu.Period = &period
			cpu.Quota = &quota
		}
		if svc.Cpuset != "" {
			cpu.Cpus = svc.Cpuset
		}
		linuxRes.CPU = cpu
		hasResources = true
	}
	if resources.PidsLimit != 0 {
		linuxRes.Pids = &specs.LinuxPids{Limit: resources.PidsLimit}
		hasResources = true
	}
	if hasResources {
		s.ResourceLimits = linuxRes
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

	res, err := containers.CreateWithSpec(p.operationContext(ctx), s, nil)
	if err != nil {
		return "", fmt.Errorf("create %s: outcome may be unknown; container %q may be retained: %w", name, containerName, err)
	}
	if err := startCreated(ctx, ctx, name, containerName, res.ID, func() error {
		return containers.Start(p.operationContext(ctx), res.ID, nil)
	}, func(cleanupCtx context.Context, id string) error {
		force := true
		result, err := containers.Remove(p.operationContext(cleanupCtx), id, &containers.RemoveOptions{Force: &force})
		return errors.Join(err, errors.Join(reports.RmReportsErrs(result)...))
	}); err != nil {
		return "", err
	}
	logger.Debug("container started", "service", name, "id", res.ID[:12])
	return res.ID, nil
}

func (p *PodmanEngine) StopService(ctx context.Context, name string) error {
	logger.Debug("stopping service", "service", name)
	timeout := uint(10)
	err := containers.Stop(p.operationContext(ctx), name, &containers.StopOptions{Timeout: &timeout})
	if podmanNotFound(err) {
		return nil
	}
	return err
}

func (p *PodmanEngine) RemoveService(ctx context.Context, name string) error {
	logger.Debug("removing container", "name", name)
	force := false
	result, err := containers.Remove(p.operationContext(ctx), name, &containers.RemoveOptions{Force: &force})
	if podmanNotFound(err) {
		err = nil
	}
	return errors.Join(err, errors.Join(reports.RmReportsErrs(result)...))
}

func (p *PodmanEngine) IsReady(ctx context.Context, name string, hasHealthCheck bool) (bool, error) {
	data, err := containers.Inspect(p.operationContext(ctx), name, nil)
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
	list, err := containers.List(p.operationContext(ctx), &containers.ListOptions{
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

func (p *PodmanEngine) ListStacks(ctx context.Context) ([]StackStatus, error) {
	filter := map[string][]string{
		"label": {"dill.managed=true"},
	}
	all := true
	list, err := containers.List(p.operationContext(ctx), &containers.ListOptions{
		All:     &all,
		Filters: filter,
	})
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*StackStatus)
	for _, c := range list {
		name := c.Labels["dill.stack"]
		if name == "" {
			continue
		}
		st := byName[name]
		if st == nil {
			st = &StackStatus{Name: name, Engine: "podman"}
			byName[name] = st
		}
		st.Containers++
		if c.State == "running" {
			st.Running++
		} else {
			st.Stopped++
		}
	}
	out := make([]StackStatus, 0, len(byName))
	for _, st := range byName {
		out = append(out, *st)
	}
	return out, nil
}

func (p *PodmanEngine) StartExisting(ctx context.Context, name string) error {
	err := containers.Start(p.operationContext(ctx), name, nil)
	if err != nil && podmanNotFound(err) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return err
}

func (p *PodmanEngine) ServiceStatus(ctx context.Context, name string) (*ContainerStatus, error) {
	data, err := containers.Inspect(p.operationContext(ctx), name, nil)
	if err != nil {
		if podmanNotFound(err) {
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
				if b.HostPort != "" && b.HostPort != "0" {
					ports = append(ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, portProto))
				}
			}
		}
	}
	sort.Strings(ports)
	return &ContainerStatus{
		Name:   strings.TrimPrefix(data.Name, "/"),
		ID:     id,
		State:  state,
		Status: state,
		Image:  data.ImageName,
		Ports:  ports,
	}, nil
}

func (p *PodmanEngine) Logs(ctx context.Context, name string, logOpts LogOptions, w io.Writer) error {
	since, err := normalizeLogSince(logOpts.Since)
	if err != nil {
		return err
	}
	tailStr := "all"
	if logOpts.Tail > 0 {
		tailStr = strconv.Itoa(logOpts.Tail)
	}
	opts := new(containers.LogOptions).
		WithFollow(logOpts.Follow).
		WithTail(tailStr).
		WithStdout(true).
		WithStderr(true)
	if logOpts.Since != "" {
		opts.WithSince(since)
	}
	if logOpts.Timestamps {
		opts.WithTimestamps(true)
	}

	stdoutCh := make(chan string, 64)
	stderrCh := make(chan string, 64)

	var wg sync.WaitGroup
	var writeMu sync.Mutex
	wg.Add(2)
	writeLines := func(ch <-chan string) {
		defer wg.Done()
		for line := range ch {
			writeMu.Lock()
			fmt.Fprint(w, line)
			writeMu.Unlock()
		}
	}
	go writeLines(stdoutCh)
	go writeLines(stderrCh)

	err = containers.Logs(p.operationContext(ctx), name, opts, stdoutCh, stderrCh)
	close(stdoutCh)
	close(stderrCh)
	wg.Wait()
	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (p *PodmanEngine) InspectConfig(ctx context.Context, name string) (*LiveConfig, error) {
	data, err := containers.Inspect(p.operationContext(ctx), name, nil)
	if err != nil {
		if podmanNotFound(err) {
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

	var networks []string
	networksKnown := data.NetworkSettings != nil && data.NetworkSettings.Networks != nil
	if networksKnown {
		for name := range data.NetworkSettings.Networks {
			networks = append(networks, name)
		}
	}
	sort.Strings(networks)

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
		if data.HostConfig.RestartPolicy != nil {
			restartPolicy = data.HostConfig.RestartPolicy.Name
		}
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

	res := config.ResourceLimits{}
	if data.HostConfig != nil {
		res = config.ResourceLimits{
			Memory:            data.HostConfig.Memory,
			MemoryReservation: data.HostConfig.MemoryReservation,
			NanoCPUs:          data.HostConfig.NanoCpus,
			CPUShares:         int64(data.HostConfig.CpuShares),
			PidsLimit:         data.HostConfig.PidsLimit,
		}
	}

	var uidMap, gidMap []string
	if data.HostConfig != nil && data.HostConfig.IDMappings != nil {
		uidMap = data.HostConfig.IDMappings.UIDMap
		gidMap = data.HostConfig.IDMappings.GIDMap
	}
	userNS := ""
	if data.HostConfig != nil {
		userNS = data.HostConfig.UsernsMode
	}
	return &LiveConfig{
		UserNSMode: userNS, UIDMap: uidMap, GIDMap: gidMap,
		MountsKnown:   data.Config != nil && data.HostConfig != nil,
		ID:            data.ID,
		Image:         data.ImageName,
		ImageID:       data.Image,
		Env:           env,
		Ports:         ports,
		Volumes:       volumes,
		RestartPolicy: restartPolicy,
		UserLabels:    userLabels,
		SystemLabels:  systemLabels,
		NetworkMode:   networkMode,
		NetworksKnown: networksKnown,
		Networks:      networks,
		User:          user,
		HealthTest:    healthTest,
		Command:       command,
		CapAdd:        capAdd,
		Init:          init,
		Cpuset:        cpuset,
		Resources:     res,
	}, nil
}

func (p *PodmanEngine) EnsureNetwork(ctx context.Context, name string) error {
	exists, err := podmannetwork.Exists(p.operationContext(ctx), name, nil)
	if err != nil {
		return fmt.Errorf("checking network: %w", err)
	}
	if exists {
		logger.Debug("network already exists", "name", name)
		return nil
	}
	_, err = podmannetwork.Create(p.operationContext(ctx), &nettypes.Network{
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
	live, err := podmannetwork.Inspect(p.operationContext(ctx), name, nil)
	if err != nil {
		if podmanNotFound(err) {
			return nil
		}
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	if live.Labels["dill.managed"] != "true" || live.Labels["dill.stack"] != name || live.ID == "" {
		return fmt.Errorf("network %q is not owned by stack %q", name, name)
	}
	return fmt.Errorf("network %q retained: Podman resolves deletion targets as names or IDs and cannot guarantee identity-safe removal; remove manually after verifying ownership", name)
}

func (p *PodmanEngine) RemoveImage(ctx context.Context, image string, force bool) error {
	opts := new(images.RemoveOptions).WithForce(force)
	_, errs := images.Remove(p.operationContext(ctx), []string{image}, opts)
	var failures []error
	for _, err := range errs {
		if err != nil && !podmanNotFound(err) {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("removing image %s: %w", image, err)
	}
	logger.Debug("image removed", "image", image)
	return nil
}

// EnsureVolume creates a stack-owned volume with dill labels if it does not
// exist. Existing volumes (including Compose-created ones) are left untouched.
func (p *PodmanEngine) EnsureVolume(ctx context.Context, name, stackName string) error {
	if p.disposable {
		if stackName == "" || name == "" {
			return ErrDisposableOwnership
		}
		live, err := podmanvolumes.Inspect(p.operationContext(ctx), name, nil)
		if err == nil {
			if live == nil || live.Name != name {
				return ErrDisposableUnavailable
			}
			return checkDisposableSettings(stackName, "local", disposableSettings{live.Driver, live.Options, live.Labels})
		}
		if !podmanNotFound(err) {
			return ErrDisposableUnavailable
		}
		created, err := podmanvolumes.Create(p.operationContext(ctx), entitiesTypes.VolumeCreateOptions{
			Name:           name,
			Labels:         map[string]string{"dill.managed": "true", "dill.stack": stackName},
			IgnoreIfExists: false,
		}, nil)
		if err != nil || created == nil || created.Name != name {
			return ErrDisposableUnavailable
		}
		return checkDisposableSettings(stackName, "local", disposableSettings{created.Driver, created.Options, created.Labels})
	}
	exists, err := podmanvolumes.Exists(p.operationContext(ctx), name, nil)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = podmanvolumes.Create(p.operationContext(ctx), entitiesTypes.VolumeCreateOptions{
		Name: name,
		Labels: map[string]string{
			"dill.managed": "true",
			"dill.stack":   stackName,
		},
		IgnoreIfExists: true,
	}, nil)
	if err != nil {
		return fmt.Errorf("creating volume %s: %w", name, err)
	}
	logger.Debug("volume created", "name", name, "stack", stackName)
	return nil
}

// ListStackVolumes returns volumes labelled as owned by the stack.
func (p *PodmanEngine) ListStackVolumes(ctx context.Context, stackName string) ([]string, error) {
	opts := new(podmanvolumes.ListOptions).WithFilters(map[string][]string{
		"label": {"dill.stack=" + stackName, "dill.managed=true"},
	})
	reports, err := podmanvolumes.List(p.operationContext(ctx), opts)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(reports))
	for _, r := range reports {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (p *PodmanEngine) RemoveVolume(ctx context.Context, name, stackName string, force bool) error {
	live, err := podmanvolumes.Inspect(p.operationContext(ctx), name, nil)
	if podmanNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect volume %s: %w", name, err)
	}
	if live == nil {
		return fmt.Errorf("inspect volume %s: empty response", name)
	}
	if live.Labels["dill.managed"] != "true" || live.Labels["dill.stack"] != stackName || stackName == "" {
		return fmt.Errorf("volume %q is not owned by stack %q; retained", name, stackName)
	}
	return fmt.Errorf("volume %q retained: safe deletion by immutable identity is unavailable; remove manually after verifying ownership", name)
}

// CLIEndpoint is the exact endpoint used by this adapter.
func (p *PodmanEngine) CLIEndpoint() string { return p.socket }

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

func (p *PodmanEngine) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	out := []PreviewResource{}
	n, err := podmannetwork.Inspect(p.operationContext(ctx), stack, nil)
	if err != nil && !podmanNotFound(err) {
		return nil, err
	}
	fact := PreviewResource{Kind: "network", Name: stack, Exists: err == nil}
	if err == nil {
		fact.ID = n.ID
		// The inspect report also contains runtime container/IP/MAC attachments.
		// Bind the complete network configuration, not that endpoint census.
		settings := n.Network
		settings.Created = time.Time{}
		fact.Settings, err = json.Marshal(settings)
		if err != nil {
			return nil, err
		}
	}
	out = append(out, fact)
	for _, name := range previewNames(names) {
		v, err := podmanvolumes.Inspect(p.operationContext(ctx), name, nil)
		if err != nil && !podmanNotFound(err) {
			return nil, err
		}
		f := PreviewResource{Kind: "volume", Name: name, Exists: err == nil}
		if err == nil {
			f.Settings, err = json.Marshal(struct {
				Name, Driver, CreatedAt, Mountpoint string
				Options, Labels                     map[string]string
			}{v.Name, v.Driver, v.CreatedAt.UTC().Format(time.RFC3339Nano), v.Mountpoint, v.Options, v.Labels})
			if err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, nil
}
