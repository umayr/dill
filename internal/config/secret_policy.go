package config

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SecretPolicy holds trusted operator metadata, never secret values.
type SecretPolicy struct {
	Version string
	Targets []SecretPolicyTarget
}
type SecretPolicyTarget struct {
	ID, Engine, Socket string
	Stacks             []SecretPolicyStack
}
type SecretPolicyStack struct {
	Name    string
	Sources []SecretPolicySource
}
type SecretPolicySource struct {
	ID, Revision, Kind, Path string
	UID, GID, Mode           int64
}

// ResolvedSecret is authorized metadata. Path must not be rendered in plans or labels.
type ResolvedSecret struct {
	Name, Source, Revision, Target string
	UID, GID, Mode                 int64
	HostPath                       string
	HostUID, HostGID, HostMode     int64
}

func secretInt(d *json.Decoder, out *int64) error {
	t, e := d.Token()
	if e != nil {
		return ErrSecretUnavailable
	}
	n, ok := t.(json.Number)
	if !ok {
		return ErrSecretUnavailable
	}
	v, e := strconv.ParseInt(string(n), 10, 64)
	if e != nil {
		return ErrSecretUnavailable
	}
	*out = v
	return nil
}
func secretArray(d *json.Decoder, f func() error) error {
	if e := secretDelim(d, '['); e != nil {
		return e
	}
	n := 0
	for d.More() {
		n++
		if n > 256 {
			return ErrSecretUnavailable
		}
		if e := f(); e != nil {
			return e
		}
	}
	return secretDelim(d, ']')
}

// ParseSecretPolicy checks strict bounded syntax and scoped uniqueness; it does not inspect files.
func ParseSecretPolicy(r io.Reader) (*SecretPolicy, error) {
	b, e := io.ReadAll(io.LimitReader(r, 65537))
	if e != nil || len(b) > 65536 || !utf8.Valid(b) {
		return nil, ErrSecretUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	p := new(SecretPolicy)
	e = secretObject(d, map[string]func() error{
		"version": func() error { return secretString(d, &p.Version) },
		"targets": func() error {
			return secretArray(d, func() error {
				var t SecretPolicyTarget
				e := secretObject(d, map[string]func() error{
					"id": func() error { return secretString(d, &t.ID) }, "engine": func() error { return secretString(d, &t.Engine) }, "socket": func() error { return secretString(d, &t.Socket) },
					"stacks": func() error {
						return secretArray(d, func() error {
							var s SecretPolicyStack
							e := secretObject(d, map[string]func() error{
								"name": func() error { return secretString(d, &s.Name) },
								"sources": func() error {
									return secretArray(d, func() error {
										var x SecretPolicySource
										e := secretObject(d, map[string]func() error{
											"id": func() error { return secretString(d, &x.ID) }, "revision": func() error { return secretString(d, &x.Revision) }, "kind": func() error { return secretString(d, &x.Kind) }, "path": func() error { return secretString(d, &x.Path) }, "uid": func() error { return secretInt(d, &x.UID) }, "gid": func() error { return secretInt(d, &x.GID) }, "mode": func() error { return secretInt(d, &x.Mode) },
										})
										if e == nil {
											s.Sources = append(s.Sources, x)
										}
										return e
									})
								},
							})
							if e == nil {
								t.Stacks = append(t.Stacks, s)
							}
							return e
						})
					},
				})
				if e == nil {
					p.Targets = append(p.Targets, t)
				}
				return e
			})
		},
	})
	if e != nil {
		return nil, ErrSecretUnavailable
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrSecretUnavailable
	}
	if err := validateSecretPolicy(p); err != nil {
		return nil, err
	}

	return p, nil
}
func validateSecretPolicy(p *SecretPolicy) error {
	if p == nil || len(p.Targets) > 256 {
		return ErrSecretUnavailable
	}
	if p.Version != "dill.secret-policy/1" {
		return ErrSecretUnavailable
	}
	ids := map[string]bool{}
	for _, t := range p.Targets {
		if len(t.Stacks) > 256 || len(t.Socket) > 128 {
			return ErrSecretUnavailable
		}
		if !secretIdentifier.MatchString(t.ID) || ids[t.ID] || (t.Engine != "docker" && t.Engine != "podman") || !cleanAbsolute(t.Socket) {
			return ErrSecretUnavailable
		}
		ids[t.ID] = true
		stacks := map[string]bool{}
		for _, s := range t.Stacks {
			if len(s.Sources) > 256 {
				return ErrSecretUnavailable
			}
			if !secretIdentifier.MatchString(s.Name) || stacks[s.Name] {
				return ErrSecretUnavailable
			}
			stacks[s.Name] = true
			sources := map[string]bool{}
			for _, x := range s.Sources {
				k := x.ID + "\x00" + x.Revision
				if !secretIdentifier.MatchString(x.ID) || !secretIdentifier.MatchString(x.Revision) || sources[k] || x.Kind != "hostFile" || len(x.Path) > 128 || !cleanAbsolute(x.Path) || x.UID < 0 || x.UID > 2147483647 || x.GID < 0 || x.GID > 2147483647 || (x.Mode != 256 && x.Mode != 288) {
					return ErrSecretUnavailable
				}
				sources[k] = true
			}
		}
	}
	return nil
}

func cleanAbsolute(s string) bool {
	return strings.HasPrefix(s, "/") && s != "/" && path.Clean(s) == s && !strings.ContainsRune(s, 0)
}

// AuthorizeSecrets resolves only exact policy tuples; no filesystem access occurs.
// Callers must separately establish policy trust, endpoint identity and source availability.
func AuthorizeSecrets(cfg *DillConfig, bindings *SecretBindings, policy *SecretPolicy, target, stack, engine, socket string) (map[string][]ResolvedSecret, error) {
	if cfg == nil || bindings == nil || validateSecretPolicy(policy) != nil || len(bindings.Bindings) > 256 || ValidateSecrets(cfg) != nil || cfg.Name == "" || cfg.Name != stack || bindings.Version != "dill.secret-bindings/1" || bindings.Target != target || bindings.Stack != stack || policy.Version != "dill.secret-policy/1" {
		return nil, ErrSecretUnavailable
	}
	var sources []SecretPolicySource
	found := false
	for _, t := range policy.Targets {
		if t.ID == target && t.Engine == engine && t.Socket == socket {
			for _, s := range t.Stacks {
				if s.Name == stack {
					if found {
						return nil, ErrSecretUnavailable
					}
					found = true
					sources = s.Sources
				}
			}
		}
	}
	if !found {
		return nil, ErrSecretUnavailable
	}
	declared := map[string]bool{}
	for _, r := range cfg.Secrets {
		declared[r.Name] = true
	}
	refs := map[string]SecretBinding{}
	for _, b := range bindings.Bindings {
		if !declared[b.Name] || refs[b.Name].Name != "" || !secretIdentifier.MatchString(b.Source) || !secretIdentifier.MatchString(b.Revision) {
			return nil, ErrSecretUnavailable
		}
		refs[b.Name] = b
	}
	result := map[string][]ResolvedSecret{}
	for name, svc := range cfg.Services {
		for _, g := range svc.Secrets {
			b, ok := refs[g.Name]
			if !ok {
				return nil, ErrSecretUnavailable
			}
			var src SecretPolicySource
			count := 0
			for _, s := range sources {
				if s.ID == b.Source && s.Revision == b.Revision {
					src = s
					count++
				}
			}
			if count != 1 || src.Kind != "hostFile" || !cleanAbsolute(src.Path) {
				return nil, ErrSecretUnavailable
			}
			result[name] = append(result[name], ResolvedSecret{Name: g.Name, Source: b.Source, Revision: b.Revision, Target: g.Target, UID: g.UID, GID: g.GID, Mode: g.Mode, HostPath: src.Path, HostUID: src.UID, HostGID: src.GID, HostMode: src.Mode})
		}
	}
	return result, nil
}
