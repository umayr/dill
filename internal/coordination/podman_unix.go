//go:build linux || darwin

package coordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/blang/semver/v4"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
)

const NativePodmanLayout = "native-local-podman-v1"
const NativePodmanNamespaceLayout = "native-local-podman-userns-v1"

type PodmanQualification struct {
	Layout, Socket, MachineID, Hostname, GraphRoot string
	Namespace                                      NamespaceProfile
}
type PodmanTarget struct {
	*Target
	apiVersion string
	socket     string
}

func (q PodmanQualification) valid() bool {
	layout := NativePodmanLayout
	if q.Namespace.Rootless {
		layout = NativePodmanNamespaceLayout
	}
	raw, err := hex.DecodeString(q.MachineID)
	if err != nil || len(raw) != 16 || strings.ToLower(q.MachineID) != q.MachineID || q.Layout != layout || !q.Namespace.Valid() || !filepath.IsAbs(q.Socket) || filepath.Clean(q.Socket) != q.Socket || !filepath.IsAbs(q.GraphRoot) || filepath.Clean(q.GraphRoot) != q.GraphRoot || len(q.Hostname) == 0 || len(q.Hostname) > 253 {
		return false
	}
	for _, c := range q.Hostname {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func PodmanIdentity(q PodmanQualification) (string, error) {
	if !q.valid() {
		return "", ErrNamespace
	}
	profile, _ := q.Namespace.Digest()
	data, _ := json.Marshal([]string{q.Layout, q.MachineID, q.Hostname, q.GraphRoot, profile})
	sum := sha256.Sum256(append([]byte("dill.qualified-podman/1\x00"), data...))
	return hex.EncodeToString(sum[:]), nil
}

// QualifyPodman uses independently provisioned host/storage/mapping identity,
// never a caller-invented daemon UUID. The layout attests native same-host,
// same-account execution with no socket proxy or alternative engine transport.
func QualifyPodman(ctx context.Context, q PodmanQualification) (*PodmanTarget, error) {
	return qualifyPodman(ctx, q, checkPodmanPeer)
}
func qualifyPodman(ctx context.Context, q PodmanQualification, verify func(*net.UnixConn, PodmanQualification) error) (*PodmanTarget, error) {
	identity, err := PodmanIdentity(q)
	if err != nil {
		return nil, ErrNamespace
	}
	target, err := newRetainedTarget(ctx, q.Socket)
	if err != nil {
		return nil, ErrNamespace
	}
	fail := func() (*PodmanTarget, error) { target.Close(); return nil, ErrNamespace }
	conn, ok := target.conn.(*net.UnixConn)
	if !ok || verify == nil || verify(conn, q) != nil {
		return fail()
	}
	// SDK uses a fixed "http://d" origin. This wrapper accepts only that exact
	// logical origin and poisons the connection on any transport/body failure.
	target.client = &http.Client{Transport: &podmanTransport{inner: target.transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("qualified target redirects forbidden") }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://d/v5.0.0/libpod/info", nil)
	response, err := target.client.Do(req)
	if err != nil {
		return fail()
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	response.Body.Close()
	var info struct {
		Host *struct {
			Hostname, OS string
			Security     *struct{ Rootless *bool }
		}
		Store   *struct{ GraphRoot string }
		Version struct{ APIVersion string }
	}
	if e != nil || len(data) > 1<<20 || response.StatusCode != 200 || json.Unmarshal(data, &info) != nil || info.Host == nil || info.Host.Security == nil || info.Host.Security.Rootless == nil || *info.Host.Security.Rootless != q.Namespace.Rootless || info.Host.OS != "linux" || info.Host.Hostname != q.Hostname || info.Store == nil || info.Store.GraphRoot != q.GraphRoot || !strings.HasPrefix(info.Version.APIVersion, "5.") {
		return fail()
	}
	target.namespaceProfile = cloneNamespace(q.Namespace)
	target.engine = "podman"
	target.identity = identity
	target.namespaceDigest, _ = q.Namespace.Digest()
	sum := sha256.Sum256([]byte("unix://" + q.Socket))
	target.endpointDigest = "sha256:" + hex.EncodeToString(sum[:])
	if v, e := semver.Parse(info.Version.APIVersion); e != nil || v.Major != 5 {
		return fail()
	}
	return &PodmanTarget{Target: target, apiVersion: info.Version.APIVersion, socket: q.Socket}, nil
}
func (t *PodmanTarget) SocketEndpoint() string {
	if t == nil {
		return ""
	}
	return "unix://" + t.socket
}

func (t *PodmanTarget) APIVersion() string {
	if t == nil {
		return ""
	}
	return t.apiVersion
}

var errPodmanConnection = errors.New("qualified Podman connection unavailable; inspect original outcome")

type podmanTransport struct {
	inner    http.RoundTripper
	mu       sync.Mutex
	poisoned bool
}

func (t *podmanTransport) poison() { t.mu.Lock(); t.poisoned = true; t.mu.Unlock() }
func (t *podmanTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" || r.URL.Host != "d" || r.URL.User != nil || (r.Host != "" && r.Host != "d") {
		return nil, errPodmanConnection
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.poisoned {
		return nil, errPodmanConnection
	}
	response, err := t.inner.RoundTrip(r)
	if err != nil {
		t.poisoned = true
		return nil, errPodmanConnection
	}
	response.Body = &podmanBody{ReadCloser: response.Body, owner: t}
	return response, nil
}

type podmanBody struct {
	io.ReadCloser
	owner *podmanTransport
}

func (b *podmanBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF {
		b.owner.poison()
	}
	return n, err
}
