//go:build linux || darwin

// Package coordination supplies local-only, unwired mutation coordination.
// No CLI or protocol mutation is protected until all entry points use it.
package coordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const NativeDockerLayout = "native-local-docker-v1"

// Qualification is operator-provisioned, not inferred from a socket spelling.
// NativeDockerLayout attests same-host/account, native callers and a qualified
// local filesystem. Container/namespace layouts are not supported. DaemonID
// must be obtained by a separate disposable-host qualification procedure.
// A caller must not fill it from the same untrusted endpoint it is qualifying.
type Qualification struct {
	Layout   string
	Socket   string
	DaemonID string
}

// Target owns one connected Docker transport. The connection used for /info
// is the ONLY connection usable for subsequent operations; no reconnect,
// redirects, proxy, context/environment endpoint fallback or socket retargeting.
// Callers must use Client and Endpoint for every engine request, never redial
// Socket. Loss of the connection requires a new qualification and operation.
type Target struct {
	client    *http.Client
	conn      net.Conn
	transport *http.Transport
	identity  string
	closeOnce sync.Once
	closeErr  error
}

const Endpoint = "http://dill-local"

func Qualify(ctx context.Context, q Qualification) (*Target, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("target qualification requires a deadline")
	}
	if q.Layout != NativeDockerLayout || !filepath.IsAbs(q.Socket) || !safeDaemonID(q.DaemonID) {
		return nil, errors.New("unsupported local target qualification")
	}
	path, err := filepath.EvalSymlinks(q.Socket)
	if err != nil {
		return nil, errors.New("local target socket unavailable")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("local target must be a Unix socket")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, errors.New("local target connection failed")
	}
	var mu sync.Mutex
	used := false
	tr := &http.Transport{MaxConnsPerHost: 1, MaxIdleConns: 1, MaxIdleConnsPerHost: 1,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			mu.Lock()
			defer mu.Unlock()
			if used {
				return nil, errors.New("qualified target connection lost; inspection required")
			}
			used = true
			return conn, nil
		},
	}
	t := &Target{conn: conn, transport: tr}
	t.client = &http.Client{Transport: localTransport{tr}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("target redirects forbidden") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Endpoint+"/info", nil)
	if err != nil {
		t.Close()
		return nil, errors.New("target qualification failed")
	}
	resp, err := t.client.Do(req)
	if err != nil {
		t.Close()
		return nil, errors.New("target qualification failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	resp.Body.Close()
	var daemon struct {
		ID string `json:"ID"`
	}
	if resp.StatusCode != http.StatusOK || readErr != nil || len(data) > 1024*1024 || json.Unmarshal(data, &daemon) != nil || daemon.ID != q.DaemonID {
		t.Close()
		return nil, errors.New("target identity does not match qualification")
	}
	sum := sha256.Sum256([]byte("dill-local-docker-v1\x00" + daemon.ID))
	t.identity = hex.EncodeToString(sum[:])
	return t, nil
}

type localTransport struct{ inner *http.Transport }

func (t localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" || r.URL.Host != "dill-local" || r.URL.User != nil || (r.Host != "" && r.Host != "dill-local") {
		return nil, errors.New("request does not use qualified target")
	}
	return t.inner.RoundTrip(r)
}

func safeDaemonID(s string) bool {
	if len(s) < 8 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func (t *Target) Client() *http.Client { return t.client }
func (t *Target) Identity() string     { return t.identity }
func (t *Target) Close() error {
	t.closeOnce.Do(func() {
		t.transport.CloseIdleConnections()
		t.closeErr = t.conn.Close()
		if errors.Is(t.closeErr, net.ErrClosed) {
			t.closeErr = nil
		}
	})
	return t.closeErr
}
