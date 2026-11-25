package orchestrator

import (
	"context"
	"errors"
	"github.com/containers/podman/v5/pkg/bindings"
	"net/http"
	"strings"
)

var ErrPrivilegeDenied = errors.New("privileged deployment requires local approval and a verified rootful Unix engine")

type privilegedKey struct{}

func privilegeAuthorized(ctx context.Context, e Engine) bool { return ctx.Value(privilegedKey{}) == e }

// AuthorizePrivileged binds this deploy context to one verified, unwrapped local adapter.
func AuthorizePrivileged(ctx context.Context, engine Engine) (context.Context, error) {
	switch e := engine.(type) {
	case *DockerEngine:
		if e.disposable || !strings.HasPrefix(e.cli.DaemonHost(), "unix:///") {
			return ctx, ErrPrivilegeDenied
		}
		info, err := e.cli.Info(ctx)
		if err != nil || info.OSType != "linux" || len(info.SecurityOptions) == 0 {
			return ctx, ErrPrivilegeDenied
		}
		for _, s := range info.SecurityOptions {
			if strings.Contains(strings.ToLower(s), "rootless") {
				return ctx, ErrPrivilegeDenied
			}
		}
	case *PodmanEngine:
		if e.disposable || !strings.HasPrefix(e.socket, "unix:///") {
			return ctx, ErrPrivilegeDenied
		}
		cctx := e.operationContext(ctx)
		c, err := bindings.GetClient(cctx)
		if err != nil {
			return ctx, ErrPrivilegeDenied
		}
		r, err := c.DoRequest(cctx, nil, http.MethodGet, "/info", nil, nil)
		if err != nil {
			return ctx, ErrPrivilegeDenied
		}
		defer r.Body.Close()
		var info struct {
			Host struct {
				Security struct {
					Rootless *bool `json:"rootless"`
				} `json:"security"`
			} `json:"host"`
		}
		if r.Process(&info) != nil || info.Host.Security.Rootless == nil || *info.Host.Security.Rootless {
			return ctx, ErrPrivilegeDenied
		}
	default:
		return ctx, ErrPrivilegeDenied
	}
	return context.WithValue(ctx, privilegedKey{}, engine), nil
}
