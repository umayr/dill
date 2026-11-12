package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/secretfile"
)

type previewTestEngine struct {
	orchestrator.Engine
	image, state, status string
	resources            []orchestrator.PreviewResource
	owned                []string
	orphan               *orchestrator.LiveConfig
}

func (e *previewTestEngine) LocalImageID(context.Context, string) (string, error) {
	return e.image, nil
}
func (e *previewTestEngine) ServiceStatus(context.Context, string) (*orchestrator.ContainerStatus, error) {
	return &orchestrator.ContainerStatus{State: e.state, Status: e.status}, nil
}
func (e *previewTestEngine) ListStackVolumes(context.Context, string) ([]string, error) {
	return e.owned, nil
}
func (e *previewTestEngine) PreviewResources(context.Context, string, []string) ([]orchestrator.PreviewResource, error) {
	return e.resources, nil
}
func (e *previewTestEngine) InspectConfig(context.Context, string) (*orchestrator.LiveConfig, error) {
	return e.orphan, nil
}

// Observation unit fixture only; actual ComputePreview still requires opaque
// checked runtime authorization. Linux integration exercises that entrypoint.
func TestPreviewStableObservationsAndInvalidation(t *testing.T) {
	collect := func(change func(*config.DillConfig, *orchestrator.LiveConfig, *previewTestEngine, *Plan, *PreviewOptions)) []byte {
		t.Helper()
		svc, _, live := secretDiffFixture(t)
		live.ID = "immutable-id"
		cfg := &config.DillConfig{Name: "demo", Engine: "docker", Services: map[string]*config.Service{"app": svc}}
		e := &previewTestEngine{image: "local-image-id", state: "running", status: "Up 1 second", resources: []orchestrator.PreviewResource{{Kind: "network", Name: "demo", ID: "network-id", Exists: true, Settings: json.RawMessage(`{"driver":"bridge"}`)}}}
		p := &Plan{Changes: []Change{{Service: "app", Kind: KindNoop}}}
		opts := PreviewOptions{}
		change(cfg, live, e, p, &opts)
		rec := &previewEngine{Engine: e, live: map[string]*orchestrator.LiveConfig{"demo_app": live}, images: map[string]string{}, stack: []string{"demo_app"}}
		state, err := observePreview(context.Background(), cfg, rec, p, opts, e, "demo")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(state)
		return b
	}
	unchanged := func(*config.DillConfig, *orchestrator.LiveConfig, *previewTestEngine, *Plan, *PreviewOptions) {}
	first := collect(unchanged)
	if !bytes.Equal(first, collect(unchanged)) {
		t.Fatal("repeated observations unstable")
	}
	if !bytes.Equal(first, collect(func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
		e.status = "Up 97 hours (healthy)"
	})) {
		t.Fatal("volatile status text entered proof")
	}
	for name, change := range map[string]func(*config.DillConfig, *orchestrator.LiveConfig, *previewTestEngine, *Plan, *PreviewOptions){
		"effective imported config": func(c *config.DillConfig, _ *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			c.Services["app"].Environment = map[string]string{"IMPORTED": "changed"}
		},
		"container identity": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.ID = "replacement"
		},
		"ownership": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.SystemLabels["dill.service"] = "other"
		},
		"mount source": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.Volumes[0].Source = "/private/changed"
		},
		"mount readonly": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.Volumes[0].ReadOnly = false
		},
		"namespace": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.UserNSMode = "changed"
		},
		"resource limits": func(_ *config.DillConfig, l *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			l.Resources.Memory = 4096
		},
		"local image": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			e.image = "moved-tag"
		},
		"lifecycle": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			e.state = "exited"
		},
		"network identity": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			e.resources[0].ID = "replacement-network"
		},
		"network settings": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			e.resources[0].Settings = []byte(`{"driver":"other"}`)
		},
		"owned volumes": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, e *previewTestEngine, _ *Plan, _ *PreviewOptions) {
			e.owned = []string{"new-volume"}
		},
		"plan": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, _ *previewTestEngine, p *Plan, _ *PreviewOptions) {
			p.Changes[0].Kind = KindRecreate
		},
		"options": func(_ *config.DillConfig, _ *orchestrator.LiveConfig, _ *previewTestEngine, _ *Plan, o *PreviewOptions) {
			o.ForceRecreate = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(first, collect(change)) {
				t.Fatal("meaningful input did not invalidate private observations")
			}
		})
	}
}
func TestPreviewCanonicalJSON(t *testing.T) {
	a, _ := secretfile.CanonicalPreviewJSON(json.RawMessage(`{"b":2,"a":{"y":1,"x":0}}`))
	b, _ := secretfile.CanonicalPreviewJSON(json.RawMessage("{ \"a\": {\"x\":0,\"y\":1}, \"b\":2 }"))
	if !bytes.Equal(a, b) {
		t.Fatal("JSON object order entered proof")
	}
}
