//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
)

func TestOperationDisposableSDKRejectsImplicitOrUnknownImageStorage(t *testing.T) {
	for _, imageConfig := range []string{`{"Id":"prepared-image","Config":{"Volumes":{"/implicit-data":{}}}}`, `{"Id":"prepared-image"}`} {
		var imageReads, writes atomic.Int32
		endpoint := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writes.Add(1)
				w.WriteHeader(500)
				return
			}
			switch {
			case strings.HasSuffix(r.URL.Path, "/_ping"):
				w.Header().Set("API-Version", "1.43")
				fmt.Fprint(w, "OK")
			case strings.Contains(r.URL.Path, "/images/synthetic/"):
				imageReads.Add(1)
				fmt.Fprint(w, imageConfig)
			case strings.HasSuffix(r.URL.Path, "/containers/json"):
				fmt.Fprint(w, `[]`)
			case strings.HasSuffix(r.URL.Path, "/containers/test_app/json"):
				w.WriteHeader(404)
				fmt.Fprint(w, `{"message":"missing"}`)
			default:
				t.Errorf("unexpected read before image preflight: %s", r.URL.Path)
				w.WriteHeader(500)
			}
		}))
		oldLoader, oldFactory := configLoader, engineFactory
		configLoader = func(context.Context, string) (*config.DillConfig, error) { return disposableConfig(), nil }
		engineFactory = defaultEngineFactory
		result, code, stderr := captureOperation(t, "", []string{"up", "--contract", operation.Version, "--engine", "docker", "--engine-socket", endpoint, "-p", "test", "--alpha-disposable"})
		configLoader, engineFactory = oldLoader, oldFactory
		if code != 1 || result.Effects != "none" || stderr != "" || imageReads.Load() == 0 || writes.Load() != 0 {
			t.Fatalf("result=%+v code=%d stderr=%q imageReads=%d writes=%d", result, code, stderr, imageReads.Load(), writes.Load())
		}
	}
}

// Exercises the actual operation decorator + SDK + disposable adapter, not a
// mock enforcer. The Unix endpoint is a disposable HTTP fake, never an engine.
func TestOperationDisposableSDKRejectsForeignVolumeAtCreation(t *testing.T) {
	var volumeReads, writes atomic.Int32
	endpoint := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writes.Add(1)
			http.Error(w, `{"message":"unexpected mutation"}`, 500)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.43")
			fmt.Fprint(w, "OK")
		case strings.HasSuffix(r.URL.Path, "/networks"):
			fmt.Fprint(w, `[{"Id":"network-id","Name":"test","Driver":"bridge","Labels":{"dill.managed":"true","dill.stack":"test"}}]`)
		case strings.Contains(r.URL.Path, "/networks/test"):
			fmt.Fprint(w, `{"Id":"network-id","Name":"test","Driver":"bridge","Labels":{"dill.managed":"true","dill.stack":"test"}}`)
		case strings.Contains(r.URL.Path, "/volumes/test_data"):
			labels := `{"dill.managed":"true","dill.stack":"test"}`
			// Initial, EnsureNetwork, actual-candidate and full-config preflight
			// see owned storage; only adapter EnsureVolume sees the replacement.
			if volumeReads.Add(1) >= 5 {
				labels = `{}`
			}
			fmt.Fprint(w, `{"Name":"test_data","Driver":"local","Labels":`+labels+`}`)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/images/synthetic/json"):
			fmt.Fprint(w, `{"Id":"image-id","Config":{}}`)
		case strings.HasSuffix(r.URL.Path, "/containers/test_app/json"):
			w.WriteHeader(404)
			fmt.Fprint(w, `{"message":"missing"}`)
		default:
			t.Errorf("unexpected fake SDK read: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			fmt.Fprint(w, `{"message":"unexpected read"}`)
		}
	}))
	oldLoader, oldFactory := configLoader, engineFactory
	t.Cleanup(func() { configLoader, engineFactory = oldLoader, oldFactory })
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		cfg := disposableConfig()
		cfg.Services["app"].PullPolicy = "never"
		return cfg, nil
	}
	engineFactory = defaultEngineFactory
	result, code, stderr := captureOperation(t, "", []string{"up", "--contract", operation.Version, "--engine", "docker", "--engine-socket", endpoint, "-p", "test", "--alpha-disposable"})
	if code != 1 || result.Outcome != "unknown" || result.Effects != "unknown" || stderr != "" || volumeReads.Load() != 5 || writes.Load() != 0 {
		t.Fatalf("result=%+v code=%d stderr=%q reads=%d writes=%d", result, code, stderr, volumeReads.Load(), writes.Load())
	}
}
