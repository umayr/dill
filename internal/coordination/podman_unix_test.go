//go:build linux || darwin

package coordination

import (
	"context"
	"fmt"
	"github.com/containers/podman/v5/pkg/bindings"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func podmanQ(t *testing.T) PodmanQualification {
	return PodmanQualification{Layout: NativePodmanLayout, Socket: filepath.Join(socketDir(t), "engine.sock"), MachineID: strings.Repeat("a", 32), Hostname: "synthetic-host", GraphRoot: "/synthetic/storage", Namespace: NamespaceProfile{UIDMap: []IDMap{{0, 0, uint32(maxID)}}, GIDMap: []IDMap{{0, 0, uint32(maxID)}}}}
}
func podmanServer(t *testing.T, q PodmanQualification, info string, mutations *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("unix", q.Socket)
	if err != nil {
		t.Fatal(err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v5.0.0/libpod/info" {
			io.WriteString(w, info)
			return
		}
		if r.Method == "POST" {
			mutations.Add(1)
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		io.WriteString(w, "ok")
	})}
	go s.Serve(l)
	t.Cleanup(func() { s.Close() })
}
func podmanInfo(host, root, security string) string {
	return fmt.Sprintf(`{"host":{"hostname":%q,"os":"linux","security":%s},"store":{"graphRoot":%q},"version":{"APIVersion":"5.0.0"}}`, host, security, root)
}
func acceptSyntheticPeer(*net.UnixConn, PodmanQualification) error { return nil }
func TestPodmanSDKCannotReplayAmbiguousMutation(t *testing.T) {
	q := podmanQ(t)
	var calls atomic.Int32
	podmanServer(t, q, podmanInfo(q.Hostname, q.GraphRoot, `{"rootless":false}`), &calls)
	ctx, cancel := bounded()
	defer cancel()
	target, err := qualifyPodman(ctx, q, acceptSyntheticPeer)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	// This is the actual pinned SDK retry loop, not a model of its behavior.
	sdk := bindings.Connection{Client: target.Client()}
	_, err = sdk.DoRequest(ctx, nil, http.MethodPost, "/containers/create", nil, nil)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("SDK retried uncertain mutation: calls=%d", calls.Load())
	}
	_, err = sdk.DoRequest(ctx, nil, http.MethodPost, "/containers/create", nil, nil)
	if err == nil || calls.Load() != 1 {
		t.Fatal("poisoned connection reused")
	}
	if target.Engine() != "podman" || target.NamespaceDigest() == "" || target.APIVersion() != "5.0.0" {
		t.Fatal("qualified profile lost")
	}
}
func TestPodmanMetadataAndIdentityDenials(t *testing.T) {
	for _, scenario := range []string{"missing", "rootless", "host", "storage", "peer", "layout"} {
		t.Run(scenario, func(t *testing.T) {
			q := podmanQ(t)
			host, root, security := q.Hostname, q.GraphRoot, `{"rootless":false}`
			verify := acceptSyntheticPeer
			switch scenario {
			case "missing":
				security = "{}"
			case "rootless":
				security = `{"rootless":true}`
			case "host":
				host = "foreign"
			case "storage":
				root = "/foreign"
			case "peer":
				verify = func(*net.UnixConn, PodmanQualification) error { return ErrNamespace }
			case "layout":
				q.Layout = NativeDockerLayout
			}
			var calls atomic.Int32
			podmanServer(t, q, podmanInfo(host, root, security), &calls)
			ctx, cancel := bounded()
			defer cancel()
			if target, err := qualifyPodman(ctx, q, verify); err == nil {
				target.Close()
				t.Fatal("unqualified Podman accepted")
			}
		})
	}
	q := podmanQ(t)
	a, err := PodmanIdentity(q)
	if err != nil {
		t.Fatal(err)
	}
	q.Socket = "/different/alias.sock"
	b, _ := PodmanIdentity(q)
	if a != b {
		t.Fatal("socket aliases changed engine identity")
	}
	q.GraphRoot = "/different/storage"
	b, _ = PodmanIdentity(q)
	if a == b {
		t.Fatal("storage retarget preserved identity")
	}
}

type failTransport struct{ calls int }

func (f *failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return nil, io.ErrUnexpectedEOF
}
func TestPodmanForeignOriginAndCancellationDoNotFallback(t *testing.T) {
	inner := &failTransport{}
	transport := &podmanTransport{inner: inner}
	r, _ := http.NewRequestWithContext(context.Background(), "GET", "http://outside.invalid", nil)
	if _, err := transport.RoundTrip(r); err == nil || inner.calls != 0 {
		t.Fatal("foreign origin escaped")
	}
	r, _ = http.NewRequestWithContext(context.Background(), "POST", "http://d/create", nil)
	transport.RoundTrip(r)
	transport.RoundTrip(r)
	if inner.calls != 1 {
		t.Fatal("failed transport used again")
	}
}
