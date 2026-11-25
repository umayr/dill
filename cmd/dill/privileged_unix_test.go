//go:build linux || darwin

package main

import (
	"context"
	"fmt"
	"github.com/umayr/dill/internal/orchestrator"
	"net/http"
	"strings"
	"testing"
)

func TestLocalPrivilegedEngineEvidence(t *testing.T) {
	for _, tc := range []struct {
		engine, info string
		allow        bool
	}{{"docker", `{"OSType":"linux","SecurityOptions":["name=seccomp"]}`, true}, {"docker", `{"OSType":"linux","SecurityOptions":["name=rootless"]}`, false}, {"docker", `{}`, false}, {"podman", `{"host":{"security":{"rootless":false}}}`, true}, {"podman", `{"host":{"security":{"rootless":true}}}`, false}, {"podman", `{}`, false}} {
		t.Run(tc.engine, func(t *testing.T) {
			endpoint := fakeUnixEngine(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					w.Header().Set("API-Version", "1.43")
					w.Header().Set("Libpod-API-Version", "5.0.0")
					fmt.Fprint(w, "OK")
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, tc.info)
					return
				}
				http.NotFound(w, r)
			}))
			ctx := context.Background()
			var e orchestrator.Engine
			var err error
			if tc.engine == "docker" {
				e, err = orchestrator.NewDockerEngineAt(endpoint)
			} else {
				e, err = orchestrator.NewPodmanEngine(ctx, endpoint)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			_, err = orchestrator.AuthorizePrivileged(ctx, e)
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%v err=%v", tc.allow, err)
			}
		})
	}
}
