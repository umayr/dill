package orchestrator

import (
	"context"
	"reflect"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/secretfile"
)

const SecretEnabledLabel = "dill.secret-enabled"

// CheckedSecretCreator is implemented by both adapters. Ordinary StartService
// deliberately remains unable to deliver grants without checked authorization.
type CheckedSecretCreator interface {
	StartServiceChecked(context.Context, string, *config.Service, string, secretfile.Request) (string, error)
}

type checkedSecretEngine struct {
	Engine
	request secretfile.Request
}

// WithSecrets routes creation through the adapter's independently repeated
// preflight. The supplied evidence must belong to this exact checked request.
func WithSecrets(e Engine, req secretfile.Request, result *secretfile.Result) (Engine, error) {
	if e == nil || result == nil || !result.AvailabilityChecked() || !result.Matches(req) {
		return nil, config.ErrSecretUnavailable
	}
	target, ok := e.(interface{ CLIEndpoint() string })
	if !ok || target.CLIEndpoint() != "unix://"+req.Socket {
		return nil, config.ErrSecretUnavailable
	}
	if _, ok := e.(CheckedSecretCreator); !ok {
		return nil, config.ErrSecretUnavailable
	}
	return &checkedSecretEngine{e, req}, nil
}
func (e *checkedSecretEngine) CLIEndpoint() string {
	return e.Engine.(interface{ CLIEndpoint() string }).CLIEndpoint()
}

func (e *checkedSecretEngine) StartService(ctx context.Context, name string, svc *config.Service, stack string) (string, error) {
	return e.Engine.(CheckedSecretCreator).StartServiceChecked(ctx, name, svc, stack, e.request)
}

func checkedSecretMounts(ctx context.Context, name string, svc *config.Service, stack, engine, endpoint string, req secretfile.Request) ([]config.ResolvedSecret, error) {
	fail := func() ([]config.ResolvedSecret, error) { return nil, config.ErrSecretUnavailable }
	if req.AuthorizationOnly || stack != req.Stack || engine != req.Engine || endpoint != "unix://"+req.Socket || req.Config == nil || req.Config.Engine != req.Engine || svc == nil {
		return fail()
	}
	if config.ValidateRuntime(req.Config) != nil {
		return fail()
	}
	expected := req.Config.Services[name]
	if expected == nil || !reflect.DeepEqual(svc.Secrets, expected.Secrets) {
		return fail()
	}
	result, err := secretfile.Preflight(ctx, req)
	if err != nil {
		return fail()
	}
	mounts, err := result.Mounts(req)
	if err != nil {
		return fail()
	}
	if len(svc.Secrets) > 0 && len(mounts[name]) != len(svc.Secrets) {
		return fail()
	}
	return mounts[name], nil
}

// DirectStartIDs classifies the entire selected existing set and verifies
// ownership before any mutation. Marker presence (including malformed values)
// blocks direct start/restart; checked up is the only secret start route.
func DirectStartIDs(ctx context.Context, e Engine, stack string, services map[string]*config.Service, full bool) (map[string]string, error) {
	fail := func() (map[string]string, error) { return nil, config.ErrSecretUnavailable }
	for _, svc := range services {
		if svc == nil || len(svc.Secrets) > 0 {
			return fail()
		}
	}
	listed, err := e.ListStack(ctx, stack)
	if err != nil {
		return fail()
	}
	ids := map[string]string{}
	for _, name := range listed {
		live, err := e.InspectConfig(ctx, name)
		if err != nil || live == nil || live.ID == "" || CheckOwnership(name, stack, live.SystemLabels["dill.service"], live) != nil {
			return fail()
		}
		_, selected := services[live.SystemLabels["dill.service"]]
		if !full && !selected {
			continue
		}
		if _, marked := live.SystemLabels[SecretEnabledLabel]; marked {
			return fail()
		}
		ids[name] = live.ID
	}
	for service, svc := range services {
		name := svc.ContainerName
		if name == "" {
			name = stack + "_" + service
		}
		live, err := e.InspectConfig(ctx, name)
		if err != nil || live == nil || live.ID == "" || CheckOwnership(name, stack, service, live) != nil {
			return fail()
		}
		if _, marked := live.SystemLabels[SecretEnabledLabel]; marked {
			return fail()
		}
		ids[name] = live.ID
	}
	return ids, nil
}
