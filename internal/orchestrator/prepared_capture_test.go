package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestPreparedRestrictionCopiedImmutableAndExhaustive(t *testing.T) {
	var p preparedImageConstraint
	if got, bound, err := p.resolve("app", "synthetic:tag"); got != "synthetic:tag" || bound || err != nil {
		t.Fatal("unbound behavior changed")
	}
	images := map[string]PreparedImage{"app": {Reference: "synthetic:tag", ID: strings.Repeat("a", 64)}}
	if err := p.install(images); err != nil {
		t.Fatal(err)
	}
	images["app"] = PreparedImage{Reference: "changed", ID: strings.Repeat("b", 64)}
	delete(images, "app")
	if got, bound, err := p.resolve("app", "synthetic:tag"); got != "sha256:"+strings.Repeat("a", 64) || !bound || err != nil {
		t.Fatal("caller changed copied restriction", got, err)
	}
	if p.install(map[string]PreparedImage{}) == nil {
		t.Fatal("compared restriction overwritten")
	}
	for _, pair := range [][2]string{{"app", "changed"}, {"other", "synthetic:tag"}} {
		if _, _, err := p.resolve(pair[0], pair[1]); err == nil {
			t.Fatal("unreviewed service/reference authorized")
		}
	}
	var empty preparedImageConstraint
	if err := empty.install(nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := empty.resolve("app", "synthetic:tag"); err == nil {
		t.Fatal("empty reviewed inventory authorized creation")
	}
	for _, id := range []string{"", "short", "synthetic:tag", "sha256:sha256:" + strings.Repeat("a", 64), strings.Repeat("A", 64)} {
		var invalid preparedImageConstraint
		if invalid.install(map[string]PreparedImage{"app": {Reference: "synthetic:tag", ID: id}}) == nil {
			t.Fatal("nonimmutable ID accepted", id)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, _, err := p.resolve("app", "synthetic:tag"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestAdapterComparedPreparedImageAtActualCreate(t *testing.T) {
	for _, adapter := range []string{"docker", "podman"} {
		for _, state := range []string{"tag-moved", "actual-id-mismatch", "implicit-volume", "absent", "wrong-reference", "unselected-service"} {
			t.Run(adapter+"/"+state, func(t *testing.T) {
				oldID := "sha256:" + strings.Repeat("a", 64)
				newID := "sha256:" + strings.Repeat("b", 64)
				var writes, inspections int
				var createdImage, createdHash string
				engine := guardrailAdapterRaw(t, adapter, contextTransport(func(r *http.Request) (*http.Response, error) {
					if strings.Contains(r.URL.Path, "/images/") {
						inspections++
						if !strings.Contains(r.URL.Path, strings.Repeat("a", 64)) {
							t.Error("adapter reselected moving reference instead of compared content")
							return contextResponse(r, 200, `{"Id":"`+newID+`","Config":{}}`), nil
						}
						if state == "absent" {
							return contextResponse(r, 404, `{"message":"missing","response":404}`), nil
						}
						actual := oldID
						if state == "actual-id-mismatch" {
							actual = newID
						}
						volume := ""
						if state == "implicit-volume" {
							volume = `"Volumes":{"/synthetic":{}}`
						}
						return contextResponse(r, 200, `{"Id":"`+actual+`","Config":{`+volume+`}}`), nil
					}
					if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create") {
						writes++
						var body struct {
							Image  string
							Labels map[string]string
						}
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						createdImage = body.Image
						createdHash = body.Labels["dill.config-hash"]
						return contextResponse(r, 201, `{"Id":"`+strings.Repeat("c", 64)+`"}`), nil
					}
					if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start") {
						writes++
						return contextResponse(r, 204, ""), nil
					}
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					return contextResponse(r, 500, `{}`), nil
				}))
				enforcer := engine.(PreparedImageEnforcer)
				if enforcer.EnforcePreparedImages(map[string]PreparedImage{}) == nil {
					t.Fatal("prepared restriction outside disposable profile accepted")
				}
				if err := engine.(DisposableVolumeEnforcer).EnforceDisposableVolumes(); err != nil {
					t.Fatal(err)
				}
				cfg := volumeOnlyConfig()
				svc := cfg.Services["app"]
				svc.Volumes = nil
				originalRef := svc.Image
				images := map[string]PreparedImage{"app": {Reference: originalRef, ID: oldID}}
				if err := enforcer.EnforcePreparedImages(images); err != nil {
					t.Fatal(err)
				}
				images["app"] = PreparedImage{Reference: "changed", ID: newID} // setter must copy
				name := "app"
				if state == "wrong-reference" {
					svc.Image = "changed"
				}
				if state == "unselected-service" {
					name = "other"
				}
				_, err := engine.StartService(context.Background(), name, svc, "stack")
				if state != "tag-moved" {
					if err == nil || writes != 0 {
						t.Fatalf("unreviewed/unsafe image reached writes: %v writes=%d", err, writes)
					}
					if (state == "wrong-reference" || state == "unselected-service") && inspections != 0 {
						t.Fatal("invalid intended service inspected before restriction")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				expectedHash, err := config.SecretServiceConfigHash("stack", "app", svc, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				if createdImage != oldID || createdHash != expectedHash || svc.Image != originalRef || writes != 2 || inspections != 1 {
					t.Fatalf("compared content/original provenance not preserved: image=%s hash=%s writes=%d inspections=%d", createdImage, createdHash, writes, inspections)
				}
			})
		}
	}
}

type preparedForwardEngine struct {
	Engine
	calls int
	got   map[string]PreparedImage
}

func (e *preparedForwardEngine) EnforcePreparedImages(images map[string]PreparedImage) error {
	e.calls++
	e.got = images
	return nil
}
func TestPreparedRestrictionDecoratorsAndMembership(t *testing.T) {
	spy := &preparedForwardEngine{}
	images := map[string]PreparedImage{"app": {Reference: "synthetic", ID: strings.Repeat("a", 64)}}
	if err := DenyMutations(spy).(PreparedImageEnforcer).EnforcePreparedImages(images); err != nil || spy.calls != 1 {
		t.Fatal("read-only restriction capability lost", err)
	}
	if !errors.Is(DenyMutations(nil).(PreparedImageEnforcer).EnforcePreparedImages(images), ErrDisposableUnavailable) {
		t.Fatal("missing capability accepted")
	}
	guard := &disposableEngine{Engine: spy, cfg: &config.DillConfig{Services: map[string]*config.Service{"app": {Image: "synthetic"}}}}
	if err := guard.EnforcePreparedImages(images); err != nil || spy.calls != 2 {
		t.Fatal("full declared restriction not forwarded", err)
	}
	for _, invalid := range []map[string]PreparedImage{nil, {}, {"other": images["app"]}, {"app": {Reference: "different", ID: strings.Repeat("a", 64)}}} {
		if guard.EnforcePreparedImages(invalid) == nil || spy.calls != 2 {
			t.Fatal("nonexhaustive/changed restriction forwarded")
		}
	}
}
