package secretfile

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/umayr/dill/internal/config"
)

// Request contains operator-selected inputs, not inputs supplied by Pkl.
// PolicyPath must come from the trusted launcher's DILL_SECRET_POLICY setting.
// Stack is the effective explicit stack name (including any CLI override).
// Services selects roots; empty means all. Dependencies are included unless
// NoDeps is set. The entire graph and all authorizations are always validated.
type Request struct {
	Config                        *config.DillConfig
	PolicyPath, BindingsPath      string
	Target, Stack, Engine, Socket string
	Services                      []string
	NoDeps                        bool
	// AuthorizationOnly is for validate: no engine contact or source inspection,
	// and no claim of runtime availability. Linux/private document checks still apply.
	AuthorizationOnly bool
}

// Result is opaque point-in-time evidence, bound to an authorized target and
// selected service references. It is not a lock, lease or permission to skip a
// fresh preflight immediately before creation. Its zero value fails closed.
type Result struct {
	bound    requestIdentity
	selected map[string]bool
	mounts   map[string][]config.ResolvedSecret
	runtime  *runtimeEvidence
	sources  map[string][]sourceObservation
	policy   *config.SecretPolicy
	bindings *config.SecretBindings
}
type requestIdentity struct{ policy, bindings, target, stack, engine, socket string }
type runtimeEvidence struct {
	mapping            Mapping
	pid                int32
	start              string
	executable, userNS fileID
}

func requestID(req Request) requestIdentity {
	return requestIdentity{req.PolicyPath, req.BindingsPath, req.Target, req.Stack, req.Engine, req.Socket}
}

// AvailabilityChecked distinguishes runtime checks from validate-only success.
func (r *Result) AvailabilityChecked() bool { return r != nil && r.runtime != nil }

// Matches reauthorizes the current private documents and compares the exact
// target, selected services and resolved references. It does not check runtime
// availability again and cannot close the external administrator race.
func (r *Result) Matches(req Request) bool {
	if r == nil || r.selected == nil || r.bound != requestID(req) || !supportedPlatform() {
		return false
	}
	next, err := authorize(req)
	return err == nil && r.sameAuthorization(next)
}

func (r *Result) sameAuthorization(next *Result) bool {
	return next != nil && reflect.DeepEqual(r.selected, next.selected) && reflect.DeepEqual(r.mounts, next.mounts) && reflect.DeepEqual(r.policy, next.policy) && reflect.DeepEqual(r.bindings, next.bindings)
}

// Mounts returns independent copies for the matching request only after a full
// runtime preflight. Authorization-only results cannot supply delivery inputs.
// Source paths must never be rendered in public plans, labels or errors.
func (r *Result) Mounts(req Request) (map[string][]config.ResolvedSecret, error) {
	if !r.AvailabilityChecked() || req.AuthorizationOnly || !r.Matches(req) {
		return nil, config.ErrSecretUnavailable
	}
	result := make(map[string][]config.ResolvedSecret, len(r.mounts))
	for name, mounts := range r.mounts {
		result[name] = append([]config.ResolvedSecret(nil), mounts...)
	}
	return result, nil
}

// Preflight is the single metadata-only entrypoint for CLI and adapters. It
// loads private documents, authorizes the whole configuration, selects the
// requested closure, and (except for AuthorizationOnly) verifies the exact
// local engine peer, its namespace mapping and every selected source/alias.
// No source contents are read; every failure has the same fixed error.
func Preflight(ctx context.Context, req Request) (*Result, error) {
	fail := func() (*Result, error) { return nil, config.ErrSecretUnavailable }
	if !supportedPlatform() || ctx == nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return fail()
	}
	result, err := authorize(req)
	if err != nil {
		return fail()
	}
	if !req.AuthorizationOnly {
		evidence, err := runtimeMapping(ctx, req.Engine, req.Socket)
		if err != nil || evidence == nil {
			return fail()
		}
		observations, err := observeSources(result.mounts, evidence.mapping)
		if err != nil {
			return fail()
		}
		result.sources = observations
		result.runtime = evidence
	}
	if ctx.Err() != nil {
		return fail()
	}
	return result, nil
}

func authorize(req Request) (*Result, error) {
	fail := func() (*Result, error) { return nil, config.ErrSecretUnavailable }
	selected, err := selectServices(req.Config, req.Services, req.NoDeps)
	if err != nil {
		return fail()
	}
	p, err := readPrivate(req.PolicyPath, uint32(os.Geteuid()))
	if err != nil {
		return fail()
	}
	policy, err := config.ParseSecretPolicy(bytes.NewReader(p))
	if err != nil {
		return fail()
	}
	b, err := readPrivate(req.BindingsPath, uint32(os.Geteuid()))
	if err != nil {
		return fail()
	}
	bindings, err := config.ParseSecretBindings(bytes.NewReader(b))
	if err != nil {
		return fail()
	}
	resolved, err := config.AuthorizeSecrets(req.Config, bindings, policy, req.Target, req.Stack, req.Engine, req.Socket)
	if err != nil {
		return fail()
	}
	for name, mounts := range resolved {
		if !selected[name] {
			delete(resolved, name)
			continue
		}
		sort.Slice(mounts, func(i, j int) bool { return mounts[i].Name < mounts[j].Name })
	}
	return &Result{bound: requestID(req), selected: selected, mounts: resolved, policy: policy, bindings: bindings}, nil
}

func selectServices(cfg *config.DillConfig, names []string, noDeps bool) (map[string]bool, error) {
	if cfg == nil || config.ValidateSecrets(cfg) != nil {
		return nil, config.ErrSecretUnavailable
	}
	// Validate the whole declared graph even when checking a subset.
	state := map[string]int{}
	var visit func(string) bool
	visit = func(name string) bool {
		svc, ok := cfg.Services[name]
		if !ok || svc == nil || state[name] == 1 {
			return false
		}
		if state[name] == 2 {
			return true
		}
		state[name] = 1
		for _, dep := range svc.DependsOn {
			if !visit(dep) {
				return false
			}
		}
		state[name] = 2
		return true
	}
	for name := range cfg.Services {
		if !visit(name) {
			return nil, config.ErrSecretUnavailable
		}
	}
	selected := map[string]bool{}
	var add func(string) bool
	add = func(name string) bool {
		svc, ok := cfg.Services[name]
		if !ok {
			return false
		}
		if selected[name] {
			return true
		}
		selected[name] = true
		if !noDeps {
			for _, dep := range svc.DependsOn {
				if !add(dep) {
					return false
				}
			}
		}
		return true
	}
	if len(names) == 0 {
		for name := range cfg.Services {
			selected[name] = true
		}
	}
	for _, name := range names {
		if !add(name) {
			return nil, config.ErrSecretUnavailable
		}
	}
	return selected, nil
}
