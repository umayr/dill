package preflight

import (
	"errors"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

var ErrEvidenceUnavailable = errors.New("complete named resource evidence unavailable")

// liveLeaf is a finite PRIVATE projection. Never serialize it as public review,
// diagnostics, history or a public artifact. A hash is not declassification.
// Image reference spelling/runtime/health/status/timestamps are not in this DTO.
type liveLeaf struct {
	HostConfigKnown bool                  `json:"hostConfigKnown"`
	HasDevices      bool                  `json:"hasDevices"`
	HasTmpfs        bool                  `json:"hasTmpfs"`
	Privileged      bool                  `json:"privileged"`
	PIDMode         string                `json:"pidMode"`
	IPCMode         string                `json:"ipcMode"`
	UTSMode         string                `json:"utsMode"`
	CgroupNSMode    string                `json:"cgroupNSMode"`
	UserNSMode      string                `json:"userNSMode"`
	UIDMap          []string              `json:"uidMap"`
	GIDMap          []string              `json:"gidMap"`
	MountsKnown     bool                  `json:"mountsKnown"`
	ID              string                `json:"id"`
	ImageID         string                `json:"imageId"`
	Environment     map[string]string     `json:"environment"`
	Ports           []config.PortBinding  `json:"ports"`
	Volumes         []config.VolumeMount  `json:"volumes"`
	RestartPolicy   string                `json:"restartPolicy"`
	UserLabels      map[string]string     `json:"userLabels"`
	SystemLabels    map[string]string     `json:"systemLabels"`
	NetworkMode     string                `json:"networkMode"`
	NetworksKnown   bool                  `json:"networksKnown"`
	Networks        []string              `json:"networks"`
	User            string                `json:"user"`
	HealthTest      []string              `json:"healthTest"`
	Command         []string              `json:"command"`
	CapAdd          []string              `json:"capAdd"`
	Init            bool                  `json:"init"`
	Cpuset          string                `json:"cpuset"`
	Resources       config.ResourceLimits `json:"resources"`
}

// ContainerDigest requires authoritative actual-name/full-ID/known metadata,
// and retains the existing ownership/secret classification boundary. It does
// NOT qualify an engine, inspect prepared images, validate the whole disposable
// profile or reserve a target. Those remain mandatory capture prerequisites.
func ContainerDigest(stack, service, requested string, live *orchestrator.LiveConfig) (string, error) {
	if live != nil {
		if _, marked := live.SystemLabels[orchestrator.SecretEnabledLabel]; marked {
			return "", config.ErrSecretUnavailable
		}
	}
	if err := orchestrator.CheckOwnership(requested, stack, service, live); err != nil {
		return "", ErrEvidenceUnavailable
	}
	if !ValidName(stack) || !ValidName(requested) || live.InspectedName != requested || !ValidID(live.ID) || !live.HostConfigKnown || !live.MountsKnown || !live.NetworksKnown {
		return "", ErrEvidenceUnavailable
	}
	image, ok := canonicalPreparedID(live.ImageID)
	if !ok {
		return "", ErrEvidenceUnavailable
	}
	v := plan.NormalizeLiveForSelection(live)
	for _, network := range v.Networks {
		if !ValidName(network) {
			return "", ErrEvidenceUnavailable
		}
	}
	leaf := liveLeaf{
		HostConfigKnown: v.HostConfigKnown, HasDevices: v.HasDevices, HasTmpfs: v.HasTmpfs, Privileged: v.Privileged,
		PIDMode: v.PIDMode, IPCMode: v.IPCMode, UTSMode: v.UTSMode, CgroupNSMode: v.CgroupNSMode, UserNSMode: v.UserNSMode, UIDMap: v.UIDMap, GIDMap: v.GIDMap,
		MountsKnown: v.MountsKnown, ID: v.ID, ImageID: image, Environment: v.Env, Ports: v.Ports, Volumes: v.Volumes, RestartPolicy: v.RestartPolicy,
		UserLabels: v.UserLabels, SystemLabels: v.SystemLabels, NetworkMode: v.NetworkMode, NetworksKnown: v.NetworksKnown, Networks: v.Networks,
		User: v.User, HealthTest: v.HealthTest, Command: v.Command, CapAdd: v.CapAdd, Init: v.Init, Cpuset: v.Cpuset, Resources: v.Resources,
	}
	return digest("dill.named-live/1", leaf)
}
