//go:build linux || darwin

package coordination

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

func TestBrokerRootfulProfileEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		want           bool
	}{
		{"rootful", `"OSType":"linux","SecurityOptions":["name=seccomp,profile=builtin","name=cgroupns"]`, true},
		{"missing", `"OSType":"linux"`, false},
		{"empty", `"OSType":"linux","SecurityOptions":[]`, false},
		{"windows", `"OSType":"windows","SecurityOptions":["name=seccomp"]`, false},
		{"rootless", `"OSType":"linux","SecurityOptions":["name=rootless","name=seccomp"]`, false},
		{"remapped", `"OSType":"linux","SecurityOptions":["name=userns","name=seccomp"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(socketDir(t), "engine.sock")
			l, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"ID":%q,%s}`, testDaemon, tc.metadata) })}
			go server.Serve(l)
			defer server.Close()
			ctx, cancel := bounded()
			defer cancel()
			target, err := Qualify(ctx, Qualification{NativeDockerLayout, path, testDaemon})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if target.BrokerRootful() != tc.want {
				t.Fatal("incorrect broker engine profile admission")
			}
		})
	}
}
