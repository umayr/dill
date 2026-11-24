package config

import (
	"fmt"
	"regexp"
	"strings"
)

type VolumeOwnership string

const (
	VolumeManaged  VolumeOwnership = "managed"
	VolumeExternal VolumeOwnership = "external"
)

type VolumeDeclaration struct {
	Ownership     VolumeOwnership   `json:"ownership"`
	Name          string            `json:"name,omitempty"`
	Driver        string            `json:"driver,omitempty"`
	DriverOptions map[string]string `json:"driverOptions,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

var volumeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func BindVolumes(cfg *DillConfig) error {
	for k, d := range cfg.Volumes {
		if !volumeID.MatchString(k) || (d.Name != "" && !volumeID.MatchString(d.Name)) {
			return fmt.Errorf("invalid volume identifier")
		}
		switch d.Ownership {
		case VolumeManaged:
		case VolumeExternal:
			if d.Name == "" || d.Driver != "" || len(d.DriverOptions)+len(d.Labels) != 0 {
				return fmt.Errorf("external volume %q requires name and forbids creation settings", k)
			}
		default:
			return fmt.Errorf("invalid volume ownership for %q", k)
		}
		for label := range d.Labels {
			if strings.HasPrefix(label, "dill.") {
				return fmt.Errorf("reserved label for volume %q", k)
			}
		}
	}
	for _, svc := range cfg.Services {
		if svc != nil {
			svc.DeclaredVolumes = cfg.Volumes
			if _, err := svc.NormalizedVolumes(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Service) NormalizedVolumes() ([]VolumeMount, error) {
	mounts, err := NormalizeVolumes(s.Volumes, s.BaseDir)
	if err != nil {
		return nil, err
	}
	for i := range mounts {
		m := &mounts[i]
		if m.Type != "volume" {
			continue
		}
		if d, ok := s.DeclaredVolumes[m.Source]; ok {
			if m.Name != "" || m.External {
				return nil, fmt.Errorf("mount %q overrides declared ownership or name", m.Source)
			}
			m.External = d.Ownership == VolumeExternal
			m.Name = d.Name
			m.Declaration = &d
		}
	}
	return mounts, nil
}
