package metricscollect

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/metricsmodel"
)

func TestSharedCallBudgetFullScrapeAndOverspend(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(fmt.Sprintf("extra_binding_read=%t", extra), func(t *testing.T) {
			var attempts, starts int
			ids := make([]metricsmodel.ContainerIdentity, MaxSamples)
			for i := range ids {
				ids[i] = owned(i)
			}
			operation := func(b ReadBudget) error {
				attempts++
				return b.Do(func(context.Context) error { starts++; return nil })
			}
			c := New(func(_ context.Context, b ReadBudget, _ string) (BoundSources, error) {
				if err := operation(b); err != nil {
					return BoundSources{}, err
				}
				if extra {
					if err := operation(b); err != nil {
						return BoundSources{}, err
					}
				}
				return BoundSources{
					Host: NewHostMemorySource(true, func(context.Context) (io.ReadCloser, error) {
						attempts++
						starts++
						return io.NopCloser(strings.NewReader(goodMeminfo)), nil
					}),
					Inventory: func(ctx context.Context, b ReadBudget, limit int) (Inventory, error) {
						if err := operation(b); err != nil {
							return Inventory{}, err
						}
						return inventory(ids...)(ctx, b, limit)
					},
					Memory: func(ctx context.Context, b ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
						err := operation(b)
						if id == ids[len(ids)-1] && extra {
							if err != ErrCallBudget {
								t.Fatalf("last sample attempt err=%v, want ErrCallBudget", err)
							}
							// Even swallowing the error cannot preserve the valid host.
							return memory(ctx, b, id)
						}
						if err != nil {
							t.Fatalf("unexpected budget refusal: %v", err)
						}
						return memory(ctx, b, id)
					},
				}, nil
			})
			s, err := c.Collect(context.Background(), daemonID)
			wantAttempts := MaxUpstreamCalls
			if extra {
				wantAttempts++
				unavailable(t, s, err)
			} else {
				if err != nil || !s.Usable() {
					t.Fatalf("exact call cap: %v", err)
				}
				checkMeasurements(t, s, true, reading(MaxSamples), reading(MaxSamples*42))
			}
			if attempts != wantAttempts || starts != MaxUpstreamCalls {
				t.Fatalf("attempts=%d starts=%d, want %d and %d", attempts, starts, wantAttempts, MaxUpstreamCalls)
			}
		})
	}
}
