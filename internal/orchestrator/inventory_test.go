package orchestrator

import (
	"context"
	"errors"
	"github.com/containers/podman/v5/pkg/bindings"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

func TestInventoryAdapters(t *testing.T) {
	for _, kind := range []string{"docker", "podman"} {
		t.Run(kind, func(t *testing.T) {
			failure := false
			lastFilters := ""
			transport := contextTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/containers/json") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				lastFilters = r.URL.Query().Get("filters")
				if failure {
					return nil, errors.New("private-token")
				}
				return contextResponse(r, 200, `[{"Id":"1","Names":["/good"],"State":"running","Labels":{"dill.managed":"true","dill.stack":"alpha","dill.service":"web"}},{"Id":"2","Names":["/other"],"State":"exited","Labels":{"dill.managed":"true","dill.stack":"beta"}},{"Id":"3","Names":["/unrelated"],"State":"running","Labels":{"dill.stack":"alpha"}},{"Id":"4","Names":["/missing"],"Labels":{"dill.managed":"true"}}]`), nil
			})
			var inv Inventory
			if kind == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.43"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				inv = &DockerEngine{cli: cli}
			} else {
				p, conn := newContextTestEngine(t, context.Background())
				conn.Client.Transport = transport
				inv = p
			}
			stacks, err := inv.InventoryStacks(context.Background())
			if err != nil || !reflect.DeepEqual(stacks, []InventoryStack{{Name: "alpha"}, {Name: "beta"}}) {
				t.Fatalf("stacks %#v: %v", stacks, err)
			}
			cs, err := inv.InventoryContainers(context.Background(), "alpha")
			if !strings.Contains(lastFilters, "dill.stack=alpha") || !strings.Contains(lastFilters, "dill.managed=true") {
				t.Fatalf("missing filters: %s", lastFilters)
			}
			if err != nil || !reflect.DeepEqual(cs, []InventoryContainer{{ID: "1", Name: "good", Service: "web", State: "running"}}) {
				t.Fatalf("containers %#v: %v", cs, err)
			}
			if _, err = inv.InventoryContainers(context.Background(), ""); !errors.Is(err, ErrInventoryStackRequired) {
				t.Fatalf("empty stack: %v", err)
			}
			failure = true
			if _, err = inv.InventoryStacks(context.Background()); !errors.Is(err, ErrInventoryUnavailable) || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unsafe error: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err = inv.InventoryStacks(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			if _, err = inv.InventoryContainers(ctx, "alpha"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
		})
	}
}

func TestInventoryPodmanCannotRedirect(t *testing.T) {
	var requests [2]atomic.Int32
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		i := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/_ping") {
				w.Header().Set("Libpod-API-Version", "5.0.0")
				return
			}
			requests[i].Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		}))
		defer servers[i].Close()
	}
	engine, err := NewPodmanEngine(context.Background(), "tcp://"+servers[0].Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := bindings.NewConnection(context.Background(), "tcp://"+servers[1].Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = engine.InventoryContainers(foreign, "alpha"); err != nil {
		t.Fatal(err)
	}
	if requests[0].Load() != 1 || requests[1].Load() != 0 {
		t.Fatalf("requests: owned=%d foreign=%d", requests[0].Load(), requests[1].Load())
	}
}

func TestInventoryLimitAndConversionCancellation(t *testing.T) {
	for _, kind := range []string{"docker", "podman"} {
		t.Run(kind, func(t *testing.T) {
			body := "[" + strings.Repeat(`{"Id":"x"},`, MaxInventoryRecords) + `{"Id":"last"}]`
			transport := contextTransport(func(r *http.Request) (*http.Response, error) { return contextResponse(r, 200, body), nil })
			var inv Inventory
			if kind == "docker" {
				cli, err := client.NewClientWithOpts(client.WithHost("http://localhost"), client.WithVersion("1.43"), client.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				inv = &DockerEngine{cli: cli}
			} else {
				p, c := newContextTestEngine(t, context.Background())
				c.Client.Transport = transport
				inv = p
			}
			got, err := inv.InventoryStacks(context.Background())
			if !errors.Is(err, ErrInventoryTooLarge) || got != nil {
				t.Fatalf("limit: %#v %v", got, err)
			}
		})
	}
	ctx := &cancelDuringConversion{Context: context.Background()}
	if got, err := inventoryContainers(ctx, make([]inventoryRecord, 10), "alpha"); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("conversion: %#v %v", got, err)
	}
}

type cancelDuringConversion struct {
	context.Context
	calls int
}

func (c *cancelDuringConversion) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}
