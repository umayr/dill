package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

// No production environment/flag override: native Docker HOST qualification
// is Linux-only. Package tests may replace this predicate to simulate the
// qualified host while exercising SDK transport on Darwin.
var qualifiedNativeHost = func() bool { return runtime.GOOS == "linux" }

var qualifiedOperations = []string{"up", "down", "stop", "start", "restart", "rm"}
var qualifiedEngines = []string{"docker"}
var qualifiedLayouts = []string{coordination.NativeDockerLayout}

func coordinationInspection(err error) bool {
	var unresolved *coordination.InspectionRequired
	return errors.As(err, &unresolved)
}

type coordinationOptions struct {
	directory, layout, daemonID, source string
	present                             bool
}

func (c *coordinationOptions) flags(fs *flag.FlagSet) {
	add := func(name, help string, dst *string) {
		fs.Func(name, help, func(value string) error { c.present = true; *dst = value; return nil })
	}
	add("coordination-state-dir", "shared private local coordination directory", &c.directory)
	add("coordination-layout", "explicit qualified layout: native-local-docker-v1", &c.layout)
	add("coordination-daemon-id", "independently qualified expected Docker daemon ID", &c.daemonID)
	add("coordination-source", "caller identity: cli or brine", &c.source)
}
func (c coordinationOptions) requested() bool { return c.present }
func (c coordinationOptions) validate(name string, opts commandOptions) error {
	supported := false
	for _, op := range qualifiedOperations {
		if name == op {
			supported = true
		}
	}
	if !qualifiedNativeHost() || !supported || opts.Engine != "docker" || !filepath.IsAbs(c.directory) || c.layout != coordination.NativeDockerLayout || c.daemonID == "" || (c.source != "cli" && c.source != "brine") {
		return usageError{"unsupported qualified coordination invocation"}
	}
	return nil
}

type operationCoordination struct {
	options         coordinationOptions
	target          *coordination.Target
	state           *coordination.State
	session         *coordination.Session
	guard           *coordinatedEngine
	operationID     string
	transportClosed bool
}

// prepare validates the complete DECLARED inventory before engine/state access.
// This first qualified slice intentionally excludes pulls/image deletion,
// external/anonymous/bind storage, special networks and undeclared orphans.
// Unsupported options never fall back to uncoordinated behavior.
func (c *operationCoordination) prepare(ctx context.Context, opts commandOptions, cfg *config.DillConfig, name, pull string) error {
	// Apply the canonical pilot's complete static boundary before opening a
	// socket. Copy policy overrides; never mutate unselected desired config.
	effective := *cfg
	effective.Services = make(map[string]*config.Service, len(cfg.Services))
	for service, svc := range cfg.Services {
		if svc == nil {
			return usageError{"unsupported qualified service layout"}
		}
		copy := *svc
		if name != "up" {
			copy.PullPolicy = "never"
		} else if pull != "" {
			copy.PullPolicy = pull
		}
		effective.Services[service] = &copy
	}
	if err := orchestrator.ValidateDisposableConfig(&effective); err != nil {
		return usageError{"unsupported qualified disposable configuration"}
	}
	stack := resolveStackNameWithOptions(cfg, opts)
	resources := []coordination.ResourceInput{{Kind: "network", Name: stack, Action: "reconcile"}}
	containers, volumes := map[string]bool{}, map[string]bool{}
	for service, svc := range cfg.Services {
		if svc == nil || svc.NetworkMode != "" {
			return usageError{"unsupported qualified service layout"}
		}
		if name == "up" && pull != "never" && !(pull == "" && svc.PullPolicy == "never") {
			return usageError{"qualified up requires prepared images and pull policy never"}
		}
		container := serviceContainerName(stack, service, svc)
		containers[container] = true
		resources = append(resources, coordination.ResourceInput{Kind: "container", Name: container, Action: "reconcile"})
		mounts, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			return usageError{"unsupported qualified storage"}
		}
		for _, mount := range mounts {
			if mount.Type == "tmpfs" {
				continue
			}
			volume := mount.EngineName(stack)
			if mount.Type != "volume" || mount.External || volume == "" {
				return usageError{"unsupported qualified storage"}
			}
			volumes[volume] = true
		}
	}
	for volume := range volumes {
		resources = append(resources, coordination.ResourceInput{Kind: "volume", Name: volume, Action: "reconcile"})
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		return resources[i].Name < resources[j].Name
	})
	if err := coordination.ValidateInventory(resources); err != nil {
		return usageError{"unsupported qualified resource inventory"}
	}
	// State changes are local metadata, separate from engine effects. Qualification
	// is read-only; the expected ID is operator assertion, not authorization.
	socket := strings.TrimPrefix(opts.EngineSocket, "unix://")
	target, err := coordination.Qualify(ctx, coordination.Qualification{Layout: c.options.layout, Socket: socket, DaemonID: c.options.daemonID})
	if err != nil {
		return errors.New("qualified target unavailable")
	}
	c.target = target
	state, err := coordination.Open(ctx, c.options.directory)
	if err != nil {
		return errors.New("trusted coordination state unavailable")
	}
	c.state = state
	c.guard = &coordinatedEngine{coord: c, stack: stack, containers: containers, volumes: volumes, ids: map[string]bool{}}
	session, err := beginCoordinatedOperation(ctx, state, target, stack, c.options.source, resources)
	if err != nil {
		var started *coordination.StartError
		if errors.As(err, &started) {
			c.operationID = started.OperationID
		}
		return err
	}
	c.session = session
	c.operationID = session.OperationID()
	engine, err := newEngine(ctx, cfg.Engine, opts)
	if err != nil {
		return err
	}
	// Whole-inventory census under the SAME locks precedes every handler. An
	// unsupported live layout blocks all writes, including no-op network ensures.
	listed, err := engine.ListStack(ctx, stack)
	if err != nil {
		return err
	}
	for _, container := range listed {
		if !containers[container] {
			return usageError{"qualified operations cannot prune undeclared containers"}
		}
	}
	actualImages, ok := c.guard.Engine.(orchestrator.DisposableImageInspector)
	if !ok {
		return orchestrator.ErrDisposableUnavailable
	}
	for service, svc := range cfg.Services {
		container := serviceContainerName(stack, service, svc)
		live, err := engine.InspectConfig(ctx, container)
		if errors.Is(err, orchestrator.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err = orchestrator.CheckOwnership(container, stack, service, live); err != nil {
			return err
		}
		modeOK := live != nil && (live.NetworkMode == "" || live.NetworkMode == "bridge" || live.NetworkMode == "default" || live.NetworkMode == stack)
		if live == nil || live.ID == "" || !live.MountsKnown || !modeOK || !live.NetworksKnown || len(live.Networks) != 1 || live.Networks[0] != stack {
			return usageError{"unsupported qualified live resource layout"}
		}
		// Ownership labels and safe desired config do not attest the existing
		// workload. Census the actual host settings before any lifecycle write.
		if !live.HostConfigKnown || live.Privileged || len(live.CapAdd) > 0 || live.HasDevices || live.HasTmpfs || live.UserNSMode != "" || len(live.UIDMap) > 0 || len(live.GIDMap) > 0 {
			return usageError{"unsupported qualified live workload"}
		}
		for _, mode := range []string{live.PIDMode, live.IPCMode, live.UTSMode, live.CgroupNSMode} {
			if mode != "" && mode != "private" {
				return usageError{"unsupported qualified live namespace"}
			}
		}
		// start/restart can execute an OLD immutable image despite a safe new
		// desired reference. Apply the same no-VOLUME pilot boundary to the
		// actual image, even when a declared owned mount covers its destination.
		if live.ImageID == "" {
			return orchestrator.ErrDisposableUnavailable
		}
		actualID, implicitVolumes, imageErr := actualImages.DisposableImage(ctx, live.ImageID)
		if imageErr != nil || actualID != live.ImageID {
			return orchestrator.ErrDisposableUnavailable
		}
		if len(implicitVolumes) > 0 {
			return orchestrator.ErrDisposableUnsupported
		}
		for _, mount := range live.Volumes {
			if mount.Type != "volume" || !volumes[mount.Source] {
				return usageError{"live storage is outside qualified inventory"}
			}
		}
		c.guard.ids[live.ID] = true
	}
	// Shared #79 whole-resource read-only preflight MUST precede every handler,
	// not merely volume creation or a caller-selected --alpha-disposable flag.
	// Its wrapper also revalidates before network/container creation and its
	// strict adapter pins the just-inspected immutable image for actual create.
	enforcer, ok := c.guard.Engine.(orchestrator.DisposableVolumeEnforcer)
	if !ok {
		return orchestrator.ErrDisposableUnavailable
	}
	c.guard.enforcer = enforcer
	guarded, err := orchestrator.WithDisposableGuardrails(ctx, c.guard.Engine, &effective, stack)
	if err != nil {
		return err
	}
	c.guard.Engine = guarded
	return nil
}

var beginCoordinatedOperation = func(ctx context.Context, state *coordination.State, target *coordination.Target, stack, source string, resources []coordination.ResourceInput) (*coordination.Session, error) {
	return state.Begin(ctx, target, stack, source, resources)
}

func (c *operationCoordination) finish(result *operation.Result) error {
	if c.target != nil && result.Target != nil {
		result.Target.QualifiedIdentityDigest = "sha256:" + c.target.Identity()
	}
	if c.operationID != "" {
		result.OperationID = c.operationID
	}
	if c.session == nil {
		return nil
	}
	result.OperationID = c.session.OperationID()
	return c.session.Finish(result.Outcome, result.Effects)
}

var closeCoordinatedTarget = func(target *coordination.Target) error { return target.Close() }
var closeCoordinatedSession = func(session *coordination.Session) error { return session.Close() }

func (c *operationCoordination) closeTransport() error {
	if c.target == nil || c.transportClosed {
		return nil
	}
	c.transportClosed = true
	return closeCoordinatedTarget(c.target)
}
func (c *operationCoordination) close() error {
	err := c.closeTransport()
	c.target = nil
	if c.session != nil {
		err = errors.Join(err, closeCoordinatedSession(c.session))
		c.session = nil
	}
	if c.state != nil {
		err = errors.Join(err, c.state.Close())
		c.state = nil
	}
	return err
}

// coordinatedEngine enforces the qualified mutation boundary defensively even
// if a handler later grows a new path. The embedded adapter retains read-only
// methods/capabilities. No raw engine diagnostics are exposed by operation mode.
type coordinatedEngine struct {
	orchestrator.Engine
	coord               *operationCoordination
	stack               string
	containers, volumes map[string]bool
	mu                  sync.Mutex
	ids                 map[string]bool
	enforcer            orchestrator.DisposableVolumeEnforcer
}

func (e *coordinatedEngine) active() error {
	if e.coord.session == nil {
		return errors.New("qualified mutation requires a durable session")
	}
	return nil
}
func (e *coordinatedEngine) allowedID(id string) error {
	if err := e.active(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.ids[id] {
		return errors.New("container is outside qualified inventory")
	}
	return nil
}
func (e *coordinatedEngine) PullImage(context.Context, string, io.Writer) error {
	return errors.New("qualified operations cannot pull images")
}
func (e *coordinatedEngine) RemoveImage(context.Context, string, bool) error {
	return errors.New("qualified operations cannot delete images")
}
func (e *coordinatedEngine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	if err := e.active(); err != nil {
		return "", err
	}
	if stack != e.stack || svc == nil || !e.containers[serviceContainerName(stack, name, svc)] || svc.NetworkMode != "" {
		return "", errors.New("service is outside qualified inventory")
	}
	mounts, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
	if err != nil {
		return "", errors.New("storage is outside qualified inventory")
	}
	for _, mount := range mounts {
		if mount.Type == "tmpfs" {
			continue
		}
		if mount.Type != "volume" || mount.External || !e.volumes[mount.EngineName(stack)] {
			return "", errors.New("storage is outside qualified inventory")
		}
	}
	id, err := e.Engine.StartService(ctx, name, svc, stack)
	if id != "" {
		e.mu.Lock()
		e.ids[id] = true
		e.mu.Unlock()
	}
	return id, err
}
func (e *coordinatedEngine) StopService(ctx context.Context, id string) error {
	if err := e.allowedID(id); err != nil {
		return err
	}
	return e.Engine.StopService(ctx, id)
}
func (e *coordinatedEngine) StartExisting(ctx context.Context, id string) error {
	if err := e.allowedID(id); err != nil {
		return err
	}
	return e.Engine.StartExisting(ctx, id)
}
func (e *coordinatedEngine) RemoveService(ctx context.Context, id string) error {
	if err := e.allowedID(id); err != nil {
		return err
	}
	return e.Engine.RemoveService(ctx, id)
}
func (e *coordinatedEngine) EnsureNetwork(ctx context.Context, name string) error {
	if err := e.active(); err != nil {
		return err
	}
	if name != e.stack {
		return errors.New("network is outside qualified inventory")
	}
	return e.Engine.EnsureNetwork(ctx, name)
}
func (e *coordinatedEngine) RemoveNetwork(ctx context.Context, name string) error {
	if err := e.active(); err != nil {
		return err
	}
	if name != e.stack {
		return errors.New("network is outside qualified inventory")
	}
	return e.Engine.RemoveNetwork(ctx, name)
}
func (e *coordinatedEngine) EnsureVolume(ctx context.Context, name, stack string) error {
	if err := e.active(); err != nil {
		return err
	}
	if stack != e.stack || !e.volumes[name] {
		return errors.New("volume is outside qualified inventory")
	}
	return e.Engine.EnsureVolume(ctx, name, stack)
}
func (e *coordinatedEngine) RemoveVolume(context.Context, string, string, bool) error {
	return errors.New("qualified operations cannot delete volumes")
}
func (e *coordinatedEngine) CLIEndpoint() string { return "" } // no subprocess escape into an unqualified transport
func (e *coordinatedEngine) EngineName() string  { return "docker" }
func (e *coordinatedEngine) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	if inspector, ok := e.Engine.(orchestrator.DisposableImageInspector); ok {
		return inspector.DisposableImage(ctx, image)
	}
	return "", nil, errors.New("prepared image qualification unavailable")
}
func (e *coordinatedEngine) EnforceDisposableVolumes() error {
	// Retain the explicit capability of the SAME raw qualified adapter even
	// after installing #79's disposable wrapper (whose Engine field is narrow).
	if e.enforcer != nil {
		return e.enforcer.EnforceDisposableVolumes()
	}
	return errors.New("disposable volume enforcement unavailable")
}
func (e *coordinatedEngine) PreviewResources(ctx context.Context, stack string, names []string) ([]orchestrator.PreviewResource, error) {
	if v, ok := e.Engine.(orchestrator.PreviewResourceInspector); ok {
		return v.PreviewResources(ctx, stack, names)
	}
	return nil, errors.New("resource inspection unavailable")
}
