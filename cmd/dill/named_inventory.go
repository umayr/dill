package main

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
)

// namedInventory holds private, invocation-local observations, never public
// output. Container evidence is copied only after the qualified raw profile
// checks; callers must not normalize it before those checks.
type namedInventory struct {
	stack      string
	listed     []string
	containers map[string]*orchestrator.LiveConfig // nil means proven NotFound
	prepared   map[string]string                   // service -> canonical immutable image ID
	resources  map[string]orchestrator.PreviewResource
}

func newNamedInventory(stack string, listed []string) *namedInventory {
	return &namedInventory{stack: stack, listed: slices.Clone(listed), containers: map[string]*orchestrator.LiveConfig{}, prepared: map[string]string{}, resources: map[string]orchestrator.PreviewResource{}}
}

func cloneNamedLive(live *orchestrator.LiveConfig) *orchestrator.LiveConfig {
	if live == nil {
		return nil
	}
	v := *live
	v.UIDMap, v.GIDMap = slices.Clone(live.UIDMap), slices.Clone(live.GIDMap)
	v.Env, v.UserLabels, v.SystemLabels = maps.Clone(live.Env), maps.Clone(live.UserLabels), maps.Clone(live.SystemLabels)
	v.Ports, v.Volumes = slices.Clone(live.Ports), slices.Clone(live.Volumes)
	v.Networks, v.HealthTest, v.Command, v.CapAdd = slices.Clone(live.Networks), slices.Clone(live.HealthTest), slices.Clone(live.Command), slices.Clone(live.CapAdd)
	return &v
}

// complete checks membership and retains the remaining evidence from the same
// qualified connection. No engine mutator is called. A missing capability,
// contradictory list/inspect pair, or incomplete SDK projection fails closed.
func (n *namedInventory) complete(ctx context.Context, engine orchestrator.Engine, cfg *config.DillConfig) error {
	bad := errNamedCaptureUnavailable
	if n == nil || cfg == nil || !preflight.ValidName(n.stack) || len(n.containers) != len(cfg.Services) {
		return bad
	}
	listed := map[string]bool{}
	for _, name := range n.listed {
		if listed[name] {
			return bad
		}
		listed[name] = true
	}
	volumeSet := map[string]bool{}
	images, ok := engine.(orchestrator.DisposableImageInspector)
	if !ok {
		return bad
	}
	for service, svc := range cfg.Services {
		if svc == nil {
			return bad
		}
		name := serviceContainerName(n.stack, service, svc)
		live, found := n.containers[name]
		if !found || listed[name] != (live != nil) {
			return bad
		}
		delete(listed, name)
		if live != nil {
			if _, err := preflight.ContainerDigest(n.stack, service, name, live); err != nil {
				return err
			}
		}
		id, volumes, err := images.DisposableImage(ctx, svc.Image)
		id = strings.TrimPrefix(id, "sha256:")
		if err != nil || !preflight.ValidID(id) || len(volumes) != 0 {
			return bad
		}
		n.prepared[service] = "sha256:" + id
		mounts, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			return bad
		}
		for _, mount := range mounts {
			if mount.Type != "volume" || mount.External {
				return bad
			}
			name := mount.EngineName(n.stack)
			if !preflight.ValidName(name) {
				return bad
			}
			volumeSet[name] = true
		}
	}
	if len(listed) != 0 {
		return bad
	}
	names := make([]string, 0, len(volumeSet))
	for name := range volumeSet {
		names = append(names, name)
	}
	sort.Strings(names)
	inspector, ok := engine.(orchestrator.PreviewResourceInspector)
	if !ok {
		return bad
	}
	facts, err := inspector.PreviewResources(ctx, n.stack, names)
	if err != nil {
		return bad
	}
	want := map[string]bool{"network\x00" + n.stack: true}
	for _, name := range names {
		want["volume\x00"+name] = true
	}
	for _, fact := range facts {
		key := fact.Kind + "\x00" + fact.Name
		if !want[key] {
			return bad
		}
		delete(want, key)
		if fact.Exists {
			if fact.Kind == "network" {
				_, err = preflight.NetworkDigest(n.stack, fact)
			} else {
				_, err = preflight.VolumeDigest(n.stack, fact)
			}
			if err != nil {
				return err
			}
		} else if fact.ID != "" || fact.ActualName != "" || len(fact.Settings) != 0 {
			return bad
		}
		fact.Settings = slices.Clone(fact.Settings)
		n.resources[key] = fact
	}
	if len(want) != 0 {
		return bad
	}
	// The qualified raw guard requires each live container to be attached to
	// exactly the stack network. An absent network or an absent mounted volume
	// contradicts that observation; do not present a complete review.
	network := n.resources["network\x00"+n.stack]
	for _, live := range n.containers {
		if live == nil {
			continue
		}
		if !network.Exists {
			return bad
		}
		for _, mount := range live.Volumes {
			volume, exists := n.resources["volume\x00"+mount.Source]
			if !exists || !volume.Exists {
				return bad
			}
		}
	}
	return ctx.Err()
}

// namedSnapshotEngine exposes only captured observations to the existing
// planner and classification helpers. Unknown lookups never fall through to a
// live engine, which would silently mix snapshots.
type namedSnapshotEngine struct {
	orchestrator.Engine
	inventory  *namedInventory
	imageByRef map[string]string
}

func (s *namedSnapshotEngine) InspectConfig(ctx context.Context, name string) (*orchestrator.LiveConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := s.inventory.containers[name]
	if !ok {
		return nil, errNamedCaptureUnavailable
	}
	if v == nil {
		return nil, orchestrator.ErrNotFound
	}
	return cloneNamedLive(v), nil
}
func (s *namedSnapshotEngine) ListStack(ctx context.Context, stack string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stack != s.inventory.stack {
		return nil, errNamedCaptureUnavailable
	}
	return slices.Clone(s.inventory.listed), nil
}
func (s *namedSnapshotEngine) LocalImageID(ctx context.Context, reference string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, ok := s.imageByRef[reference]
	if !ok {
		return "", errNamedCaptureUnavailable
	}
	return id, nil
}
func (n *namedInventory) snapshot(cfg *config.DillConfig) (*namedSnapshotEngine, error) {
	refs := map[string]string{}
	for name, svc := range cfg.Services {
		id, ok := n.prepared[name]
		if !ok || svc == nil {
			return nil, errNamedCaptureUnavailable
		}
		if prior, exists := refs[svc.Image]; exists && prior != id {
			return nil, errors.New("inconsistent prepared image evidence")
		}
		refs[svc.Image] = id
	}
	return &namedSnapshotEngine{Engine: orchestrator.DenyMutations(nil), inventory: n, imageByRef: refs}, nil
}
