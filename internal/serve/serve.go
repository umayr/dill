// Package serve implements the deliberately narrow local status API.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/umayr/dill/core"
)

const maxResponse = 1 << 20

type API struct {
	Source        core.StatusSource
	Stack, Engine string
	Stager        *Stager
}
type statusResponse struct {
	Stack      string                  `json:"stack"`
	Engine     string                  `json:"engine"`
	Containers []*core.ContainerStatus `json:"containers"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func reply(w http.ResponseWriter, status int, v any) {
	replyWithLimit(w, status, v, maxResponse)
}

func replyWithLimit(w http.ResponseWriter, status int, v any, limit int) {
	b, err := json.Marshal(v)
	if err != nil || len(b)+1 > limit {
		status = 500
		b = []byte(`{"code":"response_too_large","message":"response unavailable"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
func (a API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.Stager != nil && r.Method == http.MethodPost && r.URL.Path == "/v1/snapshots" {
		a.Stager.handle(w, r)
		return
	}
	if r.Method != http.MethodGet {
		reply(w, 405, apiError{"method_not_allowed", "only GET is supported"})
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		reply(w, 400, apiError{"invalid_request", "query parameters and bodies are not supported"})
		return
	}
	switch r.URL.Path {
	case "/v1/health":
		capabilities := []string{"health", "status"}
		if a.Stager != nil {
			capabilities = append(capabilities, "snapshot-staging")
			if a.Stager.catalog != nil {
				capabilities = append(capabilities, "snapshot-manifest")
			}
		}
		reply(w, 200, map[string]any{"api_version": "v1", "capabilities": capabilities})
	case "/v1/status":
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := core.Status(ctx, a.Source, a.Stack, a.Engine, "", 0)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				reply(w, 504, apiError{"timeout", "status timed out"})
			} else {
				reply(w, 502, apiError{"engine_error", "status unavailable"})
			}
			return
		}
		containers := make([]*core.ContainerStatus, 0, len(result.Containers))
		for _, container := range result.Containers {
			if container == nil {
				containers = append(containers, nil)
				continue
			}
			copy := *container
			if copy.State == "error" {
				copy.Status = "container status unavailable"
			}
			containers = append(containers, &copy)
		}
		reply(w, 200, statusResponse{Stack: result.Stack, Engine: result.Engine, Containers: containers})
	default:
		if a.Stager != nil && a.Stager.catalog != nil && strings.HasPrefix(r.URL.Path, "/v1/snapshots/") {
			a.Stager.manifest(w, r, strings.TrimPrefix(r.URL.Path, "/v1/snapshots/"))
			return
		}
		reply(w, 404, apiError{"unknown_operation", "operation not supported"})
	}
}

// Listen requires an existing private directory owned by the current user.
// It never removes an existing pathname and only cleans up its own socket.
func Listen(path string) (*net.UnixListener, func(), error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return nil, nil, fmt.Errorf("socket path must be absolute and clean")
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return nil, nil, fmt.Errorf("socket directory must be owned by current user and mode 0700")
	}
	// Reject symlinked ancestor components as well.
	for p := dir; p != "/"; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil || i.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("socket directory path contains symlink or inaccessible component")
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, nil, fmt.Errorf("socket path already exists")
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, err
	}
	l.SetUnlinkOnClose(false)
	own, err := os.Lstat(path)
	if err != nil {
		l.Close()
		return nil, nil, err
	}
	cleanup := func() {
		if current, e := os.Lstat(path); e == nil && os.SameFile(own, current) {
			_ = os.Remove(path)
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		l.Close()
		cleanup()
		return nil, nil, err
	}
	return l, cleanup, nil
}

// ValidateEndpoint permits only explicit local Unix URLs, with no URL options.
func ValidateEndpoint(engine, endpoint string) error {
	if engine != "docker" && engine != "podman" {
		return fmt.Errorf("engine must be docker or podman")
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return fmt.Errorf("endpoint must be an absolute unix:/// URL")
	}
	p := strings.TrimPrefix(endpoint, "unix://")
	if filepath.Clean(p) != p || strings.ContainsAny(p, "?#%") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid unix endpoint")
	}
	return nil
}

func Server(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
}
