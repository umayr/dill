package plan

import (
	"maps"
	"slices"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

// NormalizeLiveForSelection copies inspected facts for a PRIVATE commitment.
// It reuses the planner's restart/network/host-IP equivalences, but does not
// discard live-only mounts, multiple port bindings or unrequested env fields.
// It is not validation, ownership proof, a second planner or desired config.
func NormalizeLiveForSelection(live *orchestrator.LiveConfig) *orchestrator.LiveConfig {
	if live == nil {
		return nil
	}
	v := *live
	v.RestartPolicy = normalizeRestart(v.RestartPolicy)
	v.NetworkMode = normalizeNetworkMode(v.NetworkMode)
	v.Env = maps.Clone(live.Env)
	if v.Env == nil {
		v.Env = map[string]string{}
	}
	v.UserLabels = maps.Clone(live.UserLabels)
	if v.UserLabels == nil {
		v.UserLabels = map[string]string{}
	}
	v.SystemLabels = maps.Clone(live.SystemLabels)
	if v.SystemLabels == nil {
		v.SystemLabels = map[string]string{}
	}
	v.UIDMap = append([]string{}, live.UIDMap...)
	v.GIDMap = append([]string{}, live.GIDMap...)
	v.HealthTest = append([]string{}, live.HealthTest...)
	v.Command = append([]string{}, live.Command...)
	v.Networks = sortedSet(live.Networks)
	v.CapAdd = sortedSet(live.CapAdd)
	v.Ports = append([]config.PortBinding{}, live.Ports...)
	for i := range v.Ports {
		v.Ports[i].HostIP = normalizeHostIP(v.Ports[i].HostIP)
		if v.Ports[i].Protocol == "" {
			v.Ports[i].Protocol = "tcp"
		}
	}
	slices.SortFunc(v.Ports, func(a, b config.PortBinding) int {
		for _, pair := range [][2]string{{a.Target, b.Target}, {a.Protocol, b.Protocol}, {a.Published, b.Published}, {a.HostIP, b.HostIP}} {
			if pair[0] < pair[1] {
				return -1
			}
			if pair[0] > pair[1] {
				return 1
			}
		}
		return 0
	})
	v.Volumes = append([]config.VolumeMount{}, live.Volumes...)
	slices.SortFunc(v.Volumes, func(a, b config.VolumeMount) int {
		for _, pair := range [][2]string{{a.Target, b.Target}, {a.Type, b.Type}, {a.Source, b.Source}, {a.Name, b.Name}} {
			if pair[0] < pair[1] {
				return -1
			}
			if pair[0] > pair[1] {
				return 1
			}
		}
		if a.ReadOnly != b.ReadOnly {
			if !a.ReadOnly {
				return -1
			}
			return 1
		}
		if a.External != b.External {
			if !a.External {
				return -1
			}
			return 1
		}
		return 0
	})
	return &v
}
func sortedSet(in []string) []string {
	out := append([]string{}, in...)
	slices.Sort(out)
	return slices.Compact(out)
}
