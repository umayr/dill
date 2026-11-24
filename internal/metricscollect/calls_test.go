package metricscollect

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/metricsmodel"
)

func TestCallBudgetCancellationBeforeAndAfterAdmission(t *testing.T) {
	upstream := upstreamBudget{callsLeft: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := ReadBudget{ctx: ctx, upstream: &upstream}
	unexpected := func(context.Context) error {
		t.Fatal("canceled operation unexpectedly started")
		return nil
	}
	if err := b.Do(unexpected); err != context.Canceled || upstream.callsLeft != 1 || upstream.breach != nil {
		t.Fatalf("cancellation before admission: err=%v remaining=%d breach=%v", err, upstream.callsLeft, upstream.breach)
	}

	ctx, cancel = context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	b.ctx = ctx
	if err := b.Do(func(callCtx context.Context) error {
		if callCtx != ctx || upstream.callsLeft != 0 {
			t.Fatal("operation did not receive the budget context or was not charged before invocation")
		}
		cancel()
		return callCtx.Err()
	}); err != context.Canceled || upstream.callsLeft != 0 || upstream.breach != nil {
		t.Fatalf("cancellation after admission: err=%v remaining=%d breach=%v", err, upstream.callsLeft, upstream.breach)
	}
	// Cancellation takes precedence over exhaustion at zero remaining. A
	// canceled follow-up is neither started nor treated as a budget breach.
	if err := b.Do(unexpected); err != context.Canceled || upstream.callsLeft != 0 || upstream.breach != nil {
		t.Fatalf("canceled follow-up: err=%v remaining=%d breach=%v", err, upstream.callsLeft, upstream.breach)
	}
}

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
					Host: newHostMemorySource(true, func(context.Context) (io.ReadCloser, error) {
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
