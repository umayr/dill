package orchestrator

import (
	"context"
	"github.com/blang/semver/v4"
	"github.com/containers/podman/v5/pkg/bindings"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"net/http"
)

// NewBrokerPodmanEngine uses SDK initialization only to obtain its public
// context/connection objects. Its bootstrap ping supplies NO identity or review
// authority. Before any inventory or mutation, replace the entire SDK client
// with the retained, no-redial/no-replay qualified transport and overwrite the
// cached API version with the one observed through that transport.
func NewBrokerPodmanEngine(ctx context.Context, target *coordination.PodmanTarget, cfg *config.DillConfig, m *brokersecret.Materialized) (*PodmanEngine, error) {
	if ctx == nil || target == nil || target.Target == nil || target.Engine() != "podman" || target.Client() == nil || m == nil || cfg == nil || cfg.Engine != "podman" || "sha256:"+target.Identity() != m.Scope().TargetDigest || target.EndpointDigest() != m.Scope().EndpointDigest || target.NamespaceDigest() == "" {
		return nil, brokersecret.ErrUnavailable
	}
	if err := checkBrokerRuntime(target.Target, m); err != nil {
		return nil, err
	}
	if err := ValidateBrokerConfig(cfg, m); err != nil {
		return nil, err
	}
	evidenceClient, err := brokerPodmanResourceClient(target.Client())
	if err != nil {
		return nil, err
	}
	conn, err := boundPodmanContext(ctx, target.SocketEndpoint(), evidenceClient, target.APIVersion())
	if err != nil {
		return nil, err
	}
	return &PodmanEngine{conn: context.WithoutCancel(conn), socket: target.SocketEndpoint(), brokerEndpoint: target.EndpointDigest(), qualifiedBroker: target}, nil
}
func boundPodmanContext(ctx context.Context, socket string, client *http.Client, apiVersion string) (context.Context, error) {
	version, err := semver.Parse(apiVersion)
	if err != nil || version.Major != 5 {
		return nil, brokersecret.ErrUnsupported
	}
	conn, err := bindings.NewConnection(ctx, socket)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	sdk, err := bindings.GetClient(conn)
	if err != nil || sdk.Client == nil {
		return nil, brokersecret.ErrUnavailable
	}
	sdk.Client.CloseIdleConnections()
	sdk.Client = client
	cached := bindings.ServiceVersion(conn)
	*cached = version
	return context.WithoutCancel(conn), nil
}
func (p *PodmanEngine) BrokerEndpointDigest() string {
	if p == nil {
		return ""
	}
	return p.brokerEndpoint
}
