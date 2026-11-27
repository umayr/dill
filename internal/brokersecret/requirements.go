package brokersecret

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/umayr/dill/internal/config"
	"sort"
)

// Requirements contains only names and grants. Configuration values, evaluated
// Pkl, host paths and provider references never enter this result.
type Requirements struct {
	Contract     string   `json:"contract"`
	Stack        string   `json:"stack"`
	Engine       string   `json:"engine"`
	ConfigDigest string   `json:"configDigest"`
	Names        []string `json:"names"`
	Grants       []Grant  `json:"grants"`
}

func Digest(data []byte) string {
	s := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(s[:])
}

// Describe evaluates no code and contacts no engine. The caller must supply
// the configuration loaded from its independently verified source capture.
func Describe(cfg *config.DillConfig, stack string) (Requirements, error) {
	if cfg == nil || !nameOK(stack) || (cfg.Engine != "docker" && cfg.Engine != "podman") || config.ValidateRuntime(cfg) != nil || len(cfg.Secrets) > MaxEntries || len(cfg.Services) > 512 {
		return Requirements{}, ErrInvalid
	}
	out := Requirements{Contract: Version, Stack: stack, Engine: cfg.Engine, Names: []string{}, Grants: []Grant{}}
	names := map[string]bool{}
	used := map[string]bool{}
	for _, r := range cfg.Secrets {
		if !nameOK(r.Name) || names[r.Name] {
			return Requirements{}, ErrInvalid
		}
		names[r.Name] = true
		out.Names = append(out.Names, r.Name)
	}
	for service, svc := range cfg.Services {
		if !nameOK(service) || svc == nil {
			return Requirements{}, ErrInvalid
		}
		for _, g := range svc.Secrets {
			out.Grants = append(out.Grants, Grant{Service: service, Name: g.Name, Target: g.Target, UID: g.UID, GID: g.GID, Mode: uint32(g.Mode)})
			used[g.Name] = true
		}
	}
	if len(out.Grants) > MaxEntries || len(used) != len(names) {
		return Requirements{}, ErrInvalid
	}
	sort.Strings(out.Names)
	sort.Slice(out.Grants, func(i, j int) bool {
		return out.Grants[i].Service+"/"+out.Grants[i].Name < out.Grants[j].Service+"/"+out.Grants[j].Name
	})
	raw, err := json.Marshal(struct {
		Domain string
		Stack  string
		Config *config.DillConfig
	}{"dill.broker-config/1", stack, cfg})
	if err != nil {
		return Requirements{}, ErrInvalid
	}
	out.ConfigDigest = Digest(raw)
	clear(raw)
	return out, nil
}

// References refreshes on-disk generation evidence and binds it to the exact
// evaluated configuration before returning private executor mount inputs.
func (m *Materialized) References(cfg *config.DillConfig, active bool) (map[string][]config.ResolvedSecret, string, error) {
	if m == nil || m.store == nil {
		return nil, "", ErrUnavailable
	}
	desc, err := Describe(cfg, m.review.Scope.Stack)
	if err != nil || desc.ConfigDigest != m.review.Scope.ConfigDigest || desc.Engine != m.review.Scope.Engine || m.IsRetirement() && (len(desc.Names) != 0 || len(desc.Grants) != 0) {
		return nil, "", ErrUnavailable
	}
	fresh, err := m.store.Resolve(m.review.Handle, m.review.Scope)
	if err != nil || active && !fresh.review.Active || len(fresh.mounts) != len(desc.Grants) {
		return nil, "", ErrUnavailable
	}
	expected := map[string]Grant{}
	for _, g := range desc.Grants {
		expected[g.Service+"/"+g.Name] = g
	}
	refs := map[string][]config.ResolvedSecret{}
	for _, mount := range fresh.mounts {
		g, ok := expected[mount.Service+"/"+mount.Name]
		if !ok || g.Target != mount.Target || g.UID != mount.UID || g.GID != mount.GID || g.Mode != mount.Mode {
			return nil, "", ErrUnavailable
		}
		delete(expected, mount.Service+"/"+mount.Name)
		refs[mount.Service] = append(refs[mount.Service], config.ResolvedSecret{Name: mount.Name, Source: mount.Name, Revision: "g-" + m.review.Scope.Generation, Target: mount.Target, UID: mount.UID, GID: mount.GID, Mode: int64(mount.Mode), HostPath: mount.HostPath, HostUID: mount.UID, HostGID: mount.GID, HostMode: int64(mount.Mode)})
	}
	if len(expected) != 0 {
		return nil, "", ErrUnavailable
	}
	return refs, "broker-" + m.review.Scope.Registration, nil
}
func (m *Materialized) Review() Review {
	if m == nil {
		return Review{}
	}
	return m.review
}

// AcceptLive authenticates an existing active generation, not merely its labels.
func (m *Materialized) AcceptLive(handle, generation string) error {
	_, err := m.authenticateLiveGeneration(handle, generation)
	return err
}

func (m *Materialized) authenticateLiveGeneration(handle, generation string) (Review, error) {
	if m == nil || m.store == nil || !validID(handle) || !validID(generation) || !m.permitsLive(handle, generation) {
		return Review{}, ErrUnavailable
	}
	review, err := m.store.activeReview(handle, m.review.Scope)
	if err != nil || review.Scope.Generation != generation {
		return Review{}, ErrUnavailable
	}
	return review, nil
}

// LiveMounts returns only authenticated active-generation mount metadata for
// a service. It is private executor evidence, never public review output.
func (m *Materialized) LiveMounts(handle, generation, service string) ([]Mount, error) {
	if !nameOK(service) {
		return nil, ErrUnavailable
	}
	review, err := m.authenticateLiveGeneration(handle, generation)
	if err != nil {
		return nil, err
	}
	old, err := m.store.Resolve(handle, review.Scope)
	if err != nil {
		return nil, err
	}
	var mounts []Mount
	for _, mount := range old.mounts {
		if mount.Service == service {
			mounts = append(mounts, mount)
		}
	}
	return mounts, nil
}

// Activate is called only after fresh selection and ownership comparison.
// It conservatively retains the generation before the first engine mutation.
func (m *Materialized) Activate() (*Materialized, error) {
	if m == nil || m.store == nil {
		return nil, ErrUnavailable
	}
	return m.store.Activate(m.review.Handle, m.review.Scope)
}

func (m *Materialized) Scope() Scope {
	if m == nil {
		return Scope{}
	}
	return m.review.Scope
}
