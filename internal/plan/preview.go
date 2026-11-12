package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/secretfile"
)

// PreviewOptions contains effective operation options, not rendering controls
// or input/output filenames. CLI plan selection is always the full stack.
type PreviewOptions struct {
	ForceRecreate, DryRun bool
	Timeout               time.Duration
}

type previewEngine struct {
	orchestrator.Engine
	live   map[string]*orchestrator.LiveConfig
	images map[string]string
	stack  []string
}

func (e *previewEngine) CLIEndpoint() string {
	return e.Engine.(interface{ CLIEndpoint() string }).CLIEndpoint()
}
func (e *previewEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	live, err := e.Engine.InspectConfig(ctx, name)
	if errors.Is(err, orchestrator.ErrNotFound) {
		e.live[name] = nil
	} else if err == nil {
		e.live[name] = live
	}
	return live, err
}
func (e *previewEngine) LocalImageID(ctx context.Context, image string) (string, error) {
	id, err := e.Engine.LocalImageID(ctx, image)
	if err == nil {
		if old, ok := e.images[image]; ok && old != id {
			return "", config.ErrSecretUnavailable
		}
		e.images[image] = id
	}
	return id, err
}
func (e *previewEngine) ListStack(ctx context.Context, stack string) ([]string, error) {
	names, err := e.Engine.ListStack(ctx, stack)
	e.stack = append([]string{}, names...)
	sort.Strings(e.stack)
	return names, err
}

// ComputePreview observes the same planner reads, then explicitly records
// absent desired images, orphan ownership, lifecycle states and resources that
// ordinary plan rendering does not show. It never performs engine mutations.
func ComputePreview(ctx context.Context, cfg *config.DillConfig, engine orchestrator.Engine, stack string, req secretfile.Request, result *secretfile.Result, options PreviewOptions) (*Plan, secretfile.PreviewState, error) {
	fail := func() (*Plan, secretfile.PreviewState, error) {
		return nil, secretfile.PreviewState{}, config.ErrSecretUnavailable
	}
	if _, ok := engine.(interface{ CLIEndpoint() string }); !ok {
		return fail()
	}
	inspector, ok := engine.(orchestrator.PreviewResourceInspector)
	if !ok {
		return fail()
	}
	e := &previewEngine{Engine: engine, live: map[string]*orchestrator.LiveConfig{}, images: map[string]string{}}
	p, err := ComputeChecked(ctx, cfg, e, stack, req, result)
	if err != nil {
		return fail()
	}
	state, err := observePreview(ctx, cfg, e, p, options, inspector, stack)
	if err != nil {
		return fail()
	}
	return p, state, nil
}

func observePreview(ctx context.Context, cfg *config.DillConfig, e *previewEngine, p *Plan, options PreviewOptions, inspector orchestrator.PreviewResourceInspector, stack string) (secretfile.PreviewState, error) {
	fail := func() (secretfile.PreviewState, error) { return secretfile.PreviewState{}, config.ErrSecretUnavailable }
	engine := e.Engine
	for i, name := range e.stack {
		if i > 0 && e.stack[i-1] == name {
			return fail()
		}
		if _, known := e.live[name]; !known {
			live, err := e.InspectConfig(ctx, name)
			if err != nil || live == nil || orchestrator.CheckOwnership(name, stack, live.SystemLabels["dill.service"], live) != nil {
				return fail()
			}
		}
	}
	type desiredService struct {
		Service   *config.Service
		Volumes   []config.VolumeMount
		Ports     []config.PortBinding
		Resources config.ResourceLimits
	}
	desired := map[string]desiredService{}
	volumeNames := []string{}
	for name, svc := range cfg.Services {
		if _, err := e.LocalImageID(ctx, svc.Image); err != nil {
			return fail()
		}
		volumes, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			return fail()
		}
		volumes = config.ResolveVolumes(volumes, stack)
		for _, m := range volumes {
			if m.Type == "volume" {
				volumeNames = append(volumeNames, m.Source)
			}
		}
		ports, err := config.NormalizePorts(svc.Ports)
		if err != nil {
			return fail()
		}
		resources, err := config.NormalizeResources(svc)
		if err != nil {
			return fail()
		}
		desired[name] = desiredService{svc, volumes, ports, resources}
	}
	states := map[string]string{}
	for name, live := range e.live {
		if live == nil {
			continue
		}
		if live.ID == "" || !live.MountsKnown {
			return fail()
		}
		status, err := engine.ServiceStatus(ctx, live.ID)
		if err != nil || status == nil || status.State == "" {
			return fail()
		}
		states[name] = status.State
		sort.Slice(live.Volumes, func(i, j int) bool { return canonicalLess(live.Volumes[i], live.Volumes[j]) })
		sort.Slice(live.Ports, func(i, j int) bool { return canonicalLess(live.Ports[i], live.Ports[j]) })
		sort.Strings(live.CapAdd)
		sort.Strings(live.UIDMap)
		sort.Strings(live.GIDMap)
	}
	resources, err := inspector.PreviewResources(ctx, stack, volumeNames)
	if err != nil {
		return fail()
	}
	owned, err := engine.ListStackVolumes(ctx, stack)
	if err != nil {
		return fail()
	}
	sort.Strings(owned)
	configuration, err := secretfile.CanonicalPreviewJSON(struct {
		Name, Engine string
		Secrets      []config.SecretRequirement
		Services     map[string]desiredService
		Options      PreviewOptions
	}{cfg.Name, cfg.Engine, cfg.Secrets, desired, options})
	if err != nil {
		return fail()
	}
	live, err := secretfile.CanonicalPreviewJSON(struct {
		Containers   map[string]*orchestrator.LiveConfig
		Images       map[string]string
		Stack        []string
		States       map[string]string
		Resources    []orchestrator.PreviewResource
		OwnedVolumes []string
	}{e.live, e.images, e.stack, states, resources, owned})
	if err != nil {
		return fail()
	}
	rendered, err := secretfile.CanonicalPreviewJSON(p)
	if err != nil {
		return fail()
	}
	return secretfile.PreviewState{Configuration: configuration, Live: live, Plan: rendered}, nil
}

func canonicalLess(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Compare(x, y) < 0
}
