package orchestrator

import (
	"context"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/umayr/dill/internal/brokersecret"
)

// Broker creation is reachable only after retained target+shared-runtime checks.
// "host" here means the qualified engine user namespace, NOT initial-host root
// when the engine is rootless. It prevents ambient Podman userns defaults from
// silently selecting keep-id/auto/custom subordinate maps after review.
func applyBrokerPodmanNamespace(ctx context.Context, s *specgen.SpecGenerator) {
	if s == nil {
		return
	}
	if _, ok := ctx.Value(brokerCreateKey{}).(brokersecret.Review); ok {
		s.UserNS = specgen.Namespace{NSMode: specgen.Host}
		// Podman otherwise defaults to shareable IPC, which the retained live
		// profile correctly refuses. Select private IPC at creation, never
		// normalize an existing shareable workload into private evidence.
		s.IpcNS = specgen.Namespace{NSMode: specgen.Private}
	}
}

// BrokerDefaultNamespace checks RAW inspect facts. Podman5.0.0 reports no OCI
// child user namespace as empty UsernsMode; omitted IDMappings means no custom
// maps. Never erase private/keep-id/maps before the native selection digest.
func BrokerDefaultNamespace(live *LiveConfig) bool {
	return live != nil && live.HostConfigKnown && live.UserNSMode == "" && len(live.UIDMap) == 0 && len(live.GIDMap) == 0
}
