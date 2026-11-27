package orchestrator

import (
	"context"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/umayr/dill/internal/brokersecret"
	"testing"
)

func TestBrokerPodmanExplicitNamespaceAndRawFactDenials(t *testing.T) {
	s := specgen.NewSpecGenerator("synthetic", false)
	applyBrokerPodmanNamespace(context.Background(), s)
	if s.UserNS.NSMode == specgen.Host {
		t.Fatal("ordinary context changed namespace")
	}
	ctx := context.WithValue(context.Background(), brokerCreateKey{}, brokersecret.Review{})
	applyBrokerPodmanNamespace(ctx, s)
	if s.UserNS.NSMode != specgen.Host || s.IpcNS.NSMode != specgen.Private {
		t.Fatal("broker inherited ambient namespace defaults")
	}
	live := &LiveConfig{HostConfigKnown: true}
	if !BrokerDefaultNamespace(live) {
		t.Fatal("pinned Podman default inspect refused")
	}
	for _, mutate := range []func(*LiveConfig){func(x *LiveConfig) { x.HostConfigKnown = false }, func(x *LiveConfig) { x.UserNSMode = "private" }, func(x *LiveConfig) { x.UserNSMode = "keep-id" }, func(x *LiveConfig) { x.UserNSMode = "host" }, func(x *LiveConfig) { x.UserNSMode = "ns:/foreign" }, func(x *LiveConfig) { x.UIDMap = []string{"0:1000:1"} }, func(x *LiveConfig) { x.GIDMap = []string{"0:1000:1"} }} {
		copy := *live
		mutate(&copy)
		if BrokerDefaultNamespace(&copy) {
			t.Fatal("custom/unknown mapping accepted")
		}
	}
}
