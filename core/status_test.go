package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/umayr/dill/core"
)

type source struct {
	names   []string
	listErr error
}

func (s source) ListStack(_ context.Context, _ string) ([]string, error) { return s.names, s.listErr }
func (s source) ServiceStatus(_ context.Context, name string) (*core.ContainerStatus, error) {
	if name == "missing" {
		return nil, errors.New("gone")
	}
	return &core.ContainerStatus{Name: name, State: "running"}, nil
}

func TestStatusPublicConsumer(t *testing.T) {
	out, err := core.Status(context.Background(), source{names: []string{"web", "missing"}}, "stack", "docker", "compose.pkl", 1)
	if err != nil {
		t.Fatal(err)
	}
	if out.Stack != "stack" || len(out.Containers) != 2 || out.Containers[0].State != "running" || out.Containers[1].State != "error" || out.Containers[1].Status != "gone" {
		t.Fatalf("unexpected status: %+v", out)
	}
}
func TestStatusEmptyAndListFailure(t *testing.T) {
	out, err := core.Status(context.Background(), source{}, "s", "docker", "c", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"containers":[]`) {
		t.Fatal(string(b))
	}
	sentinel := errors.New("offline")
	_, err = core.Status(context.Background(), source{listErr: sentinel}, "s", "docker", "c", 0)
	if !errors.Is(err, sentinel) || err.Error() != "listing stack: offline" {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestOpenStatusEngineRequiresExplicitEndpoint(t *testing.T) {
	if _, err := core.OpenStatusEngine(context.Background(), "docker", ""); err == nil {
		t.Fatal("accepted implicit endpoint")
	}
	if _, err := core.OpenStatusEngine(context.Background(), "unknown", "/tmp/socket"); err == nil {
		t.Fatal("accepted unknown engine")
	}
}

// callbackSource permits cancellation at precise engine boundaries.
type callbackSource struct {
	list    func(context.Context) ([]string, error)
	inspect func(context.Context, string) (*core.ContainerStatus, error)
}

func (s callbackSource) ListStack(ctx context.Context, _ string) ([]string, error) {
	return s.list(ctx)
}
func (s callbackSource) ServiceStatus(ctx context.Context, name string) (*core.ContainerStatus, error) {
	return s.inspect(ctx, name)
}

func TestStatusCancellation(t *testing.T) {
	for _, phase := range []string{"before list", "during list", "during inspection", "wrapped canceled", "wrapped deadline"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lists, inspections := 0, 0
			if phase == "before list" {
				cancel()
			}
			s := callbackSource{
				list: func(context.Context) ([]string, error) {
					lists++
					if phase == "during list" {
						cancel()
					}
					return []string{"one", "two"}, nil
				},
				inspect: func(_ context.Context, name string) (*core.ContainerStatus, error) {
					inspections++
					switch phase {
					case "during inspection":
						cancel()
						return &core.ContainerStatus{Name: name}, nil
					case "wrapped canceled":
						return nil, fmt.Errorf("private: %w", context.Canceled)
					case "wrapped deadline":
						return nil, fmt.Errorf("private: %w", context.DeadlineExceeded)
					}
					return &core.ContainerStatus{Name: name}, nil
				},
			}
			result, err := core.Status(ctx, s, "s", "docker", "c", 1)
			want := context.Canceled
			if phase == "wrapped deadline" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || len(result.Containers) != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			wantLists, wantInspections := 1, 1
			if phase == "before list" {
				wantLists, wantInspections = 0, 0
			}
			if phase == "during list" {
				wantInspections = 0
			}
			if lists != wantLists || inspections != wantInspections {
				t.Fatalf("lists=%d inspections=%d", lists, inspections)
			}
		})
	}
}
