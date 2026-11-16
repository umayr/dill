package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/umayr/dill/core"
)

type downFake struct {
	calls  []string
	bad    bool
	fail   error
	cancel context.CancelFunc
}

func (f *downFake) ListStack(ctx context.Context, stack string) ([]string, error) {
	f.calls = append(f.calls, "list")
	return []string{"a", "b"}, nil
}
func (f *downFake) InspectConfig(ctx context.Context, name string) (*core.LiveConfig, error) {
	f.calls = append(f.calls, "inspect:"+name)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stack := "stack"
	if f.bad && name == "a" {
		stack = "other"
	}
	return &core.LiveConfig{ID: name + "-id", SystemLabels: map[string]string{"dill.managed": "true", "dill.stack": stack, "dill.service": name}}, nil
}
func (f *downFake) StopService(ctx context.Context, id string) error {
	f.calls = append(f.calls, "stop:"+id)
	if f.cancel != nil {
		f.cancel()
	}
	return f.fail
}
func (f *downFake) RemoveService(ctx context.Context, id string) error {
	f.calls = append(f.calls, "remove:"+id)
	return f.fail
}
func (f *downFake) RemoveNetwork(ctx context.Context, name string) error {
	f.calls = append(f.calls, "network:"+name)
	return ctx.Err()
}
func TestDownCapturesBeforeMutationAndReportsPartial(t *testing.T) {
	f := &downFake{bad: true, fail: errors.New("failed")}
	result, err := core.Down(context.Background(), f, "stack", nil)
	if err == nil {
		t.Fatal("expected joined failure")
	}
	want := []string{"list", "inspect:a", "inspect:b", "inspect:b", "stop:b-id", "inspect:b", "remove:b-id", "network:stack"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v", f.calls)
	}
	if len(result.Events) != 4 || result.Events[0].Err == nil || result.Events[2].Err == nil {
		t.Fatalf("events %#v", result.Events)
	}
}
func TestDownCancellationAndEmptyStack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &downFake{cancel: cancel}
	result, err := core.Down(ctx, f, "stack", nil)
	if !errors.Is(err, context.Canceled) || len(result.Events) == 0 {
		t.Fatalf("result %#v err %v", result, err)
	}
	for _, call := range f.calls {
		if call == "remove:a-id" || call == "stop:b-id" {
			t.Fatalf("mutation after cancellation: %v", f.calls)
		}
	}
	f = &downFake{}
	if _, err := core.Down(context.Background(), f, "", nil); err == nil || len(f.calls) != 0 {
		t.Fatalf("empty stack: %v %v", err, f.calls)
	}
}
func TestOpenDownEngineRequiresEndpoint(t *testing.T) {
	if _, err := core.OpenDownEngine(context.Background(), "docker", ""); err == nil {
		t.Fatal("missing endpoint")
	}
	if _, err := core.OpenDownEngine(context.Background(), "other", "/tmp/x"); err == nil {
		t.Fatal("unknown engine")
	}
}
