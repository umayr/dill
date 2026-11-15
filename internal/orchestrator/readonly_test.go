package orchestrator

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestReadOnlyEngineDeniesEveryMutatorWithoutDelegate(t *testing.T) {
	// A nil underlying Engine panics if ANY call accidentally delegates.
	e := DenyMutations(nil)
	ctx := context.Background()
	calls := map[string]func() error{
		"PullImage":     func() error { return e.PullImage(ctx, "synthetic", io.Discard) },
		"StartService":  func() error { _, err := e.StartService(ctx, "web", &config.Service{}, "synthetic"); return err },
		"StopService":   func() error { return e.StopService(ctx, "id") },
		"StartExisting": func() error { return e.StartExisting(ctx, "id") },
		"RemoveService": func() error { return e.RemoveService(ctx, "id") },
		"EnsureNetwork": func() error { return e.EnsureNetwork(ctx, "synthetic") },
		"RemoveNetwork": func() error { return e.RemoveNetwork(ctx, "synthetic") },
		"RemoveImage":   func() error { return e.RemoveImage(ctx, "id", true) },
		"EnsureVolume":  func() error { return e.EnsureVolume(ctx, "data", "synthetic") },
		"RemoveVolume":  func() error { return e.RemoveVolume(ctx, "data", "synthetic", true) },
	}
	reads := map[string]bool{"ImageExists": true, "LocalImageID": true, "IsReady": true, "ServiceStatus": true, "Logs": true, "InspectConfig": true, "ListStack": true, "ListStacks": true, "ListStackVolumes": true, "Close": true}
	iface := reflect.TypeOf((*Engine)(nil)).Elem()
	if iface.NumMethod() != len(calls)+len(reads) {
		t.Fatal("Engine changed: classify every new method before capture support")
	}
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i).Name
		if _, ok := calls[m]; !ok && !reads[m] {
			t.Fatal("unclassified engine method", m)
		}
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(call(), ErrReadOnlyEngine) {
				t.Fatal("mutation not denied before delegate")
			}
		})
	}
	if _, _, err := e.(DisposableImageInspector).DisposableImage(ctx, "synthetic"); !errors.Is(err, ErrDisposableUnsupported) {
		t.Fatal("unknown image evidence invented")
	}
	if _, err := e.(PreviewResourceInspector).PreviewResources(ctx, "synthetic", nil); !errors.Is(err, ErrDisposableUnsupported) {
		t.Fatal("unknown resource evidence invented")
	}
	if err := e.(DisposableVolumeEnforcer).EnforceDisposableVolumes(); !errors.Is(err, ErrDisposableUnsupported) {
		t.Fatal("unknown enforcement capability invented")
	}
}

type readOnlyEvidenceProbe struct {
	Engine
	ctx           context.Context
	calls, closed int
}

func (e *readOnlyEvidenceProbe) check(ctx context.Context) {
	if ctx != e.ctx {
		panic("context not forwarded")
	}
	e.calls++
}
func (e *readOnlyEvidenceProbe) InspectConfig(ctx context.Context, name string) (*LiveConfig, error) {
	e.check(ctx)
	return &LiveConfig{ID: "full-identity", InspectedName: name}, nil
}
func (e *readOnlyEvidenceProbe) DisposableImage(ctx context.Context, image string) (string, []string, error) {
	e.check(ctx)
	return image, []string{"/explicit-image-volume"}, nil
}
func (e *readOnlyEvidenceProbe) PreviewResources(ctx context.Context, stack string, names []string) ([]PreviewResource, error) {
	e.check(ctx)
	return []PreviewResource{{Kind: "network", Name: stack, ActualName: "actual-not-lookup", ID: "full-network-identity", Exists: true}, {Kind: "volume", Name: names[0], ActualName: names[0], Exists: true}}, nil
}
func (*readOnlyEvidenceProbe) EnforceDisposableVolumes() error { return nil }
func (*readOnlyEvidenceProbe) EngineName() string              { return "docker" }
func (*readOnlyEvidenceProbe) CLIEndpoint() string             { return "unix:///synthetic-only.sock" }
func (e *readOnlyEvidenceProbe) Close() error {
	e.closed++
	return errors.New("synthetic close failure")
}
func TestReadOnlyEngineForwardsPrivateEvidenceAndActualClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &readOnlyEvidenceProbe{ctx: ctx}
	e := DenyMutations(probe)
	live, err := e.InspectConfig(ctx, "requested")
	if err != nil || live.InspectedName != "requested" {
		t.Fatal("live evidence lost")
	}
	id, volumes, err := e.(DisposableImageInspector).DisposableImage(ctx, "prepared-id")
	if err != nil || id != "prepared-id" || len(volumes) != 1 {
		t.Fatal("prepared metadata filtered or invented")
	}
	facts, err := e.(PreviewResourceInspector).PreviewResources(ctx, "lookup", []string{"data"})
	if err != nil || len(facts) != 2 || facts[0].ActualName != "actual-not-lookup" || facts[1].ActualName != "data" {
		t.Fatal("authoritative names replaced by selector")
	}
	if probe.calls != 3 {
		t.Fatal("read-only evidence not delegated")
	}
	if err := e.Close(); err == nil || probe.closed != 1 {
		t.Fatal("actual transport cleanup failure hidden")
	}
	if e.(interface{ EngineName() string }).EngineName() != "docker" || e.(interface{ CLIEndpoint() string }).CLIEndpoint() != "unix:///synthetic-only.sock" {
		t.Fatal("retained selector identity lost")
	}
}
