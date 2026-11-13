//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

// This is an HTTP simulator, not a real daemon. Creation bodies are captured and
// fed back through each real SDK's InspectConfig representation before planning.
type pinnedImageHTTPState struct {
	mu                                     sync.Mutex
	content, container, createdImage, user string
	labels                                 map[string]string
	creates, stops, removes                int
}

func (s *pinnedImageHTTPState) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/_ping"):
		w.Header().Set("API-Version", "1.43")
		w.Header().Set("Libpod-API-Version", "5.0.0")
		fmt.Fprint(w, "OK")
	case strings.Contains(r.URL.Path, "/images/") && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"Id": s.content, "Config": map[string]any{}})
	case strings.HasSuffix(r.URL.Path, "/networks/stack/exists"):
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/networks"):
		fmt.Fprint(w, `[{"Id":"network-id","Name":"stack","Driver":"bridge","Labels":{"dill.managed":"true","dill.stack":"stack"}}]`)
	case strings.HasSuffix(r.URL.Path, "/networks/stack/json"), strings.HasSuffix(r.URL.Path, "/networks/stack"):
		fmt.Fprint(w, `{"Id":"network-id","Name":"stack","Driver":"bridge","Labels":{"dill.managed":"true","dill.stack":"stack"}}`)
	case strings.HasSuffix(r.URL.Path, "/containers/create") && r.Method == http.MethodPost:
		var body struct {
			Image, User string
			Labels      map[string]string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		s.creates++
		s.container = fmt.Sprintf("created-container-%04d", s.creates)
		s.createdImage, s.user, s.labels = body.Image, body.User, body.Labels
		if s.createdImage != s.content {
			t.Errorf("creation did not pin inspected content: %q != %q", s.createdImage, s.content)
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"Id": s.container})
	case strings.HasSuffix(r.URL.Path, "/containers/json"):
		fmt.Fprint(w, `[]`)
	case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") && r.Method == http.MethodGet:
		if s.container == "" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"missing","response":404}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"Id": s.container, "Name": "/stack_app", "Image": s.createdImage, "ImageName": s.createdImage,
			"Config":     map[string]any{"Image": s.createdImage, "Labels": s.labels, "User": s.user},
			"HostConfig": map[string]any{"NetworkMode": "bridge"}, "NetworkSettings": map[string]any{"Ports": map[string]any{}},
			"State": map[string]any{"Running": true, "Status": "running"}, "Mounts": []any{},
		})
	case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
		w.WriteHeader(204)
	case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/stop") && r.Method == http.MethodPost:
		s.stops++
		w.WriteHeader(204)
	case strings.Contains(r.URL.Path, "/containers/") && r.Method == http.MethodDelete:
		s.removes++
		s.container = ""
		fmt.Fprint(w, `[]`)
	default:
		t.Errorf("unexpected synthetic engine request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
		fmt.Fprint(w, `{"message":"unexpected","response":500}`)
	}
}

func TestPinnedImageCreateInspectPlanAndRepeatUpConverge(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, change := range []string{"unchanged", "moved-tag", "changed-reference", "changed-config"} {
			t.Run(adapter+"/"+change, func(t *testing.T) {
				id := "sha256:" + strings.Repeat("a", 64)
				state := &pinnedImageHTTPState{content: id}
				endpoint := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { state.serve(t, w, r) }))
				cfg := &config.DillConfig{Name: "stack", Engine: adapter, Services: map[string]*config.Service{"app": {Image: "synthetic:tag", PullPolicy: "never"}}}
				ctx := context.Background()
				open := func(ctx context.Context, _ *config.DillConfig) (orchestrator.Engine, error) {
					engine, err := defaultEngineFactory(ctx, adapter, endpoint)
					if err != nil {
						return nil, err
					}
					guarded, err := orchestrator.WithDisposableGuardrails(ctx, engine, cfg, "stack")
					if err != nil {
						engine.Close()
					}
					return guarded, err
				}
				// Both adapters exercise the guarded library; Podman is deliberately
				// not advertised as an Alpha CLI profile target.
				engine, err := open(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer engine.Close()
				if _, err := engine.StartService(ctx, "app", cfg.Services["app"], "stack"); err != nil {
					t.Fatal(err)
				}
				live, err := engine.InspectConfig(ctx, "stack_app")
				if err != nil {
					t.Fatal(err)
				}
				if live.Image != id || live.ImageID != id {
					t.Fatalf("inspect lost actual pinned representation: %+v", live)
				}
				assertPlan := func(want plan.ChangeKind, imageDrift bool) {
					t.Helper()
					p, err := plan.Compute(ctx, cfg, engine, "stack")
					if err != nil {
						t.Fatal(err)
					}
					if len(p.Changes) != 1 || p.Changes[0].Kind != want {
						t.Fatalf("plan=%+v", p.Changes)
					}
					if imageDrift {
						found := false
						for _, d := range p.Changes[0].Diffs {
							if d.Field == "image_id" {
								found = true
							}
						}
						if !found {
							t.Fatalf("moved image content not detected: %+v", p.Changes)
						}
					}
				}
				assertPlan(plan.KindNoop, false)
				opts := commandOptions{Quiet: true, NoPrune: true, Timeout: time.Second}
				if err := runUpConfig(ctx, opts, cfg, nil, false, "", open); err != nil {
					t.Fatal(err)
				}
				state.mu.Lock()
				creates, stops, removes := state.creates, state.stops, state.removes
				state.mu.Unlock()
				if creates != 1 || stops != 0 || removes != 0 {
					t.Fatalf("identical up recreated: creates=%d stops=%d removes=%d", creates, stops, removes)
				}
				switch change {
				case "moved-tag":
					state.mu.Lock()
					state.content = "sha256:" + strings.Repeat("b", 64)
					state.mu.Unlock()
				case "changed-reference":
					cfg.Services["app"].Image = "synthetic:alias-same-content"
				case "changed-config":
					cfg.Services["app"].User = "1000"
				}
				if change == "unchanged" {
					assertPlan(plan.KindNoop, false)
					return
				}
				assertPlan(plan.KindRecreate, change == "moved-tag")
				if err := runUpConfig(ctx, opts, cfg, nil, false, "", open); err != nil {
					t.Fatal(err)
				}
				state.mu.Lock()
				creates, stops, removes = state.creates, state.stops, state.removes
				state.mu.Unlock()
				if creates != 2 || stops != 1 || removes != 1 {
					t.Fatalf("real drift not applied: creates=%d stops=%d removes=%d", creates, stops, removes)
				}
				assertPlan(plan.KindNoop, false)
			})
		}
	}
}
