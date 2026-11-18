package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/umayr/dill/internal/hostagent"
)

func (d dockerIdentity) Inspect(ctx context.Context, id string) (hostagent.InspectResult, error) {
	var out hostagent.InspectResult
	req, e := http.NewRequestWithContext(ctx, "GET", "http://engine/containers/"+id+"/json", nil)
	if e != nil {
		return out, e
	}
	resp, e := d.client.Do(req)
	if e != nil {
		return out, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return out, hostagent.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return out, errors.New("engine unavailable")
	}
	var v struct {
		ID      string `json:"Id"`
		Name    string `json:"Name"`
		Image   string `json:"Image"`
		Created string `json:"Created"`
		Config  struct {
			Image       string            `json:"Image"`
			User        string            `json:"User"`
			Env         []string          `json:"Env"`
			Entrypoint  []string          `json:"Entrypoint"`
			Cmd         []string          `json:"Cmd"`
			Labels      map[string]string `json:"Labels"`
			Healthcheck *struct {
				Test []string `json:"Test"`
			} `json:"Healthcheck"`
		} `json:"Config"`
		State struct {
			Status string `json:"Status"`
			Health *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
		HostConfig struct {
			RestartPolicy struct {
				Name string `json:"Name"`
			} `json:"RestartPolicy"`
			Memory      int64    `json:"Memory"`
			NanoCPUs    int64    `json:"NanoCpus"`
			PidsLimit   *int64   `json:"PidsLimit"`
			Privileged  bool     `json:"Privileged"`
			CapAdd      []string `json:"CapAdd"`
			NetworkMode string   `json:"NetworkMode"`
			PidMode     string   `json:"PidMode"`
			IpcMode     string   `json:"IpcMode"`
		} `json:"HostConfig"`
		Mounts []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
		NetworkSettings struct {
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
			Networks map[string]json.RawMessage `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if e = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(&v); e != nil {
		return out, e
	}
	p := hostagent.InspectProfile{ID: v.ID, Name: strings.TrimPrefix(v.Name, "/"), Image: v.Config.Image, ImageID: v.Image, State: v.State.Status, Command: []string{}, EnvKeys: []string{}, Labels: map[string]string{}, Ports: []hostagent.InspectPort{}, Mounts: []hostagent.InspectMount{}, Networks: []string{}, Health: hostagent.InspectHealth{Test: []string{}}, Security: hostagent.InspectSecurity{CapAdd: []string{}}}
	if v.Created != "" {
		p.CreatedAt = &v.Created
	}
	if v.Config.User != "" {
		p.User = &v.Config.User
	}
	p.Command = append(p.Command, v.Config.Entrypoint...)
	p.Command = append(p.Command, v.Config.Cmd...)
	seen := map[string]bool{}
	for _, entry := range v.Config.Env {
		k, _, ok := strings.Cut(entry, "=")
		if !ok || k == "" || seen[k] {
			return out, errors.New("invalid environment")
		}
		seen[k] = true
		p.EnvKeys = append(p.EnvKeys, k)
	}
	sort.Strings(p.EnvKeys)
	for key, bindings := range v.NetworkSettings.Ports {
		port, protocol, ok := strings.Cut(key, "/")
		n, e := strconv.ParseUint(port, 10, 16)
		if !ok || e != nil || protocol == "" {
			return out, errors.New("invalid port")
		}
		if len(bindings) == 0 {
			p.Ports = append(p.Ports, hostagent.InspectPort{Protocol: protocol, ContainerPort: uint16(n)})
			continue
		}
		for _, binding := range bindings {
			var h uint64
			if binding.HostPort != "" {
				h, e = strconv.ParseUint(binding.HostPort, 10, 16)
				if e != nil {
					return out, e
				}
			}
			p.Ports = append(p.Ports, hostagent.InspectPort{Protocol: protocol, HostIP: binding.HostIP, HostPort: uint16(h), ContainerPort: uint16(n)})
		}
	}
	sort.Slice(p.Ports, func(i, j int) bool {
		a, b := p.Ports[i], p.Ports[j]
		if a.ContainerPort != b.ContainerPort {
			return a.ContainerPort < b.ContainerPort
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		if a.HostIP != b.HostIP {
			return a.HostIP < b.HostIP
		}
		return a.HostPort < b.HostPort
	})
	for _, m := range v.Mounts {
		p.Mounts = append(p.Mounts, hostagent.InspectMount{Type: m.Type, Source: m.Source, Destination: m.Destination, ReadOnly: !m.RW})
	}
	for n := range v.NetworkSettings.Networks {
		p.Networks = append(p.Networks, n)
	}
	sort.Strings(p.Networks)
	if v.HostConfig.RestartPolicy.Name != "" {
		p.RestartPolicy = &v.HostConfig.RestartPolicy.Name
	}
	if v.State.Health != nil {
		p.Health.Status = &v.State.Health.Status
	}
	if v.Config.Healthcheck != nil {
		p.Health.Test = append(p.Health.Test, v.Config.Healthcheck.Test...)
	}
	if v.HostConfig.Memory >= 0 {
		p.Resources.MemoryBytes = &v.HostConfig.Memory
	}
	if v.HostConfig.NanoCPUs >= 0 {
		p.Resources.CPUNano = &v.HostConfig.NanoCPUs
	}
	if v.HostConfig.PidsLimit != nil && *v.HostConfig.PidsLimit >= 0 {
		p.Resources.PidsLimit = v.HostConfig.PidsLimit
	}
	p.Security.Privileged = &v.HostConfig.Privileged
	p.Security.CapAdd = append(p.Security.CapAdd, v.HostConfig.CapAdd...)
	if v.HostConfig.NetworkMode != "" {
		p.Security.NetworkMode = &v.HostConfig.NetworkMode
	}
	if v.HostConfig.PidMode != "" {
		p.Security.PIDMode = &v.HostConfig.PidMode
	}
	if v.HostConfig.IpcMode != "" {
		p.Security.IPCMode = &v.HostConfig.IpcMode
	}
	out.Profile = p
	out.Labels = v.Config.Labels
	return out, nil
}
