package metricscollect

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/umayr/dill/internal/metricsmodel"
)

const daemonID = "enrolled-fixture"

func current() metricsmodel.SourceTime {
	return metricsmodel.SourceTime{At: time.Now(), Verified: true}
}

func reading(v uint64) metricsmodel.UintReading {
	return metricsmodel.UintReading{Value: v, Present: true}
}

func host(_ context.Context, _ ReadBudget) (metricsmodel.HostObservation, error) {
	return metricsmodel.HostObservation{
		LocalityVerified: true, SameRead: true, SourceTime: current(),
		TotalKB: reading(100), AvailableKB: reading(20),
	}, nil
}

func owned(i int) metricsmodel.ContainerIdentity {
	return metricsmodel.ContainerIdentity{
		ID: fmt.Sprintf("id-%d", i), Incarnation: "generation-1",
		State: metricsmodel.StateRunning, Ownership: metricsmodel.OwnershipDill,
	}
}

func inventory(ids ...metricsmodel.ContainerIdentity) func(context.Context, ReadBudget, int) (Inventory, error) {
	return func(context.Context, ReadBudget, int) (Inventory, error) {
		return Inventory{Exhaustive: true, SourceTime: current(), Candidates: append([]metricsmodel.ContainerIdentity(nil), ids...)}, nil
	}
}

func memory(_ context.Context, _ ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
	return metricsmodel.ContainerMemoryObservation{Identity: id, SourceTime: current(), UsageBytes: reading(42)}, nil
}

func collector(t *testing.T, sources BoundSources) *Collector {
	t.Helper()
	return New(func(_ context.Context, _ ReadBudget, expected string) (BoundSources, error) {
		if expected != daemonID {
			t.Errorf("unexpected enrolled ID: %q", expected)
			return BoundSources{}, ErrDaemonUnverified
		}
		return sources, nil
	})
}

func collect(t *testing.T, c *Collector) metricsmodel.Snapshot {
	t.Helper()
	s, err := c.Collect(context.Background(), daemonID)
	if err != nil || !s.Usable() {
		t.Fatalf("expected usable snapshot, got %v (%v)", s, err)
	}
	return s
}

func unavailable(t *testing.T, s metricsmodel.Snapshot, err error) {
	t.Helper()
	if s != (metricsmodel.Snapshot{}) || err != ErrUnavailable {
		t.Fatalf("expected zero snapshot and ErrUnavailable, got %v (%v)", s, err)
	}
}

func checkMeasurements(t *testing.T, s metricsmodel.Snapshot, hostOK bool, count, usage metricsmodel.UintReading) {
	t.Helper()
	if total, avail, ok := s.HostMemoryBytes(); ok != hostOK || (ok && (total != 100*1024 || avail != 20*1024)) {
		t.Errorf("host = %d, %d, %v; want fixture pair present = %v", total, avail, ok, hostOK)
	}
	if value, ok := s.OwnedRunningContainers(); (metricsmodel.UintReading{Value: value, Present: ok}) != count {
		t.Errorf("count = %d, %v; want %+v", value, ok, count)
	}
	if value, ok := s.OwnedMemoryUsageBytes(); (metricsmodel.UintReading{Value: value, Present: ok}) != usage {
		t.Errorf("usage = %d, %v; want %+v", value, ok, usage)
	}
}

func TestCollectFreshAndIndependent(t *testing.T) {
	stopped, foreign := owned(2), owned(3)
	stopped.State, stopped.Ownership = metricsmodel.StateStopped, metricsmodel.OwnershipUnknown
	foreign.Ownership = metricsmodel.OwnershipNotOwned
	var verifies, samples int
	c := New(func(_ context.Context, _ ReadBudget, expected string) (BoundSources, error) {
		verifies++
		if expected != daemonID || verifies == 4 {
			return BoundSources{Host: host}, ErrDaemonUnverified // Never use callbacks from failed verification.
		}
		s := BoundSources{
			Host:      host,
			Inventory: inventory(stopped, owned(1), foreign),
			Memory: func(ctx context.Context, b ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
				samples++
				if id != owned(1) {
					t.Errorf("sampled ineligible identity: %+v", id)
				}
				return memory(ctx, b, id)
			},
		}
		switch verifies {
		case 2:
			s.Host = func(ctx context.Context, b ReadBudget) (metricsmodel.HostObservation, error) {
				h, _ := host(ctx, b)
				h.SourceTime.At = h.SourceTime.At.Add(-time.Minute)
				return h, nil
			}
		case 3:
			s.Inventory = nil
		}
		return s, nil
	})
	first := collect(t, c)
	checkMeasurements(t, first, true, reading(1), reading(42))
	checkMeasurements(t, collect(t, c), false, reading(1), reading(42))
	checkMeasurements(t, collect(t, c), true, metricsmodel.UintReading{}, metricsmodel.UintReading{})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
	checkMeasurements(t, first, true, reading(1), reading(42))
	if verifies != 4 || samples != 2 {
		t.Fatalf("verification calls = %d, sample calls = %d", verifies, samples)
	}
}

func TestInventoryProofBeforeSampling(t *testing.T) {
	badState, badOwner, missingID, missingIncarnation := owned(2), owned(2), owned(2), owned(2)
	badState.State = metricsmodel.StateUnknown
	badOwner.Ownership = metricsmodel.OwnershipUnknown
	missingID.ID = ""
	missingIncarnation.Incarnation = ""
	large := make([]metricsmodel.ContainerIdentity, MaxCandidates+1)
	for i := range large {
		large[i] = owned(i)
	}
	// Exactly the candidate limit with an independently proven exhaustive
	// list is fine; the distinct sample cap still applies.
	for _, tc := range []struct {
		name  string
		ids   []metricsmodel.ContainerIdentity
		tweak func(*Inventory)
		count metricsmodel.UintReading
	}{
		{name: "incomplete empty", tweak: func(i *Inventory) { i.Exhaustive = false }},
		{name: "stale empty", tweak: func(i *Inventory) { i.SourceTime.At = i.SourceTime.At.Add(-time.Minute) }},
		{name: "unverified empty", tweak: func(i *Inventory) { i.SourceTime.Verified = false }},
		{name: "future inventory", tweak: func(i *Inventory) { i.SourceTime.At = i.SourceTime.At.Add(time.Hour) }},
		{name: "empty", count: reading(0)},
		{name: "unknown state", ids: []metricsmodel.ContainerIdentity{owned(1), badState}},
		{name: "unknown ownership", ids: []metricsmodel.ContainerIdentity{owned(1), badOwner}},
		{name: "missing ID", ids: []metricsmodel.ContainerIdentity{owned(1), missingID}},
		{name: "duplicate ID", ids: []metricsmodel.ContainerIdentity{owned(1), owned(1)}},
		{name: "missing incarnation", ids: []metricsmodel.ContainerIdentity{owned(1), missingIncarnation}, count: reading(2)},
		{name: "over sample cap", ids: large[:MaxSamples+1], count: reading(MaxSamples + 1)},
		{name: "at candidate cap", ids: large[:MaxCandidates], count: reading(MaxCandidates)},
		{name: "over candidate cap", ids: large},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := collector(t, BoundSources{
				Host: host,
				Inventory: func(ctx context.Context, b ReadBudget, limit int) (Inventory, error) {
					if limit != MaxCandidates {
						t.Fatalf("candidate ceiling = %d", limit)
					}
					i, _ := inventory(tc.ids...)(ctx, b, limit)
					if tc.tweak != nil {
						tc.tweak(&i)
					}
					return i, nil
				},
				Memory: func(context.Context, ReadBudget, metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
					t.Fatal("sampled without a qualified nonempty population within the cap")
					return metricsmodel.ContainerMemoryObservation{}, nil
				},
			})
			checkMeasurements(t, collect(t, c), true, tc.count, metricsmodel.UintReading{})
		})
	}
}

func TestSampleProofAndCountOnlyRefusal(t *testing.T) {
	for _, tc := range []struct {
		name          string
		edit          func(*metricsmodel.ContainerMemoryObservation)
		err           error
		contradiction bool
	}{
		{name: "absent field", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.UsageBytes.Present = false }},
		{name: "stale", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.SourceTime.At = r.SourceTime.At.Add(-time.Minute) }},
		{name: "future", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.SourceTime.At = r.SourceTime.At.Add(time.Minute) }},
		{name: "unverified time", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.SourceTime.Verified = false }},
		{name: "different container", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.Identity.ID = "another" }},
		{name: "restarted", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.Identity.Incarnation = "generation-2" }},
		{name: "stopped", edit: func(r *metricsmodel.ContainerMemoryObservation) { r.Identity.State = metricsmodel.StateStopped }},
		{name: "contradictory owner", edit: func(r *metricsmodel.ContainerMemoryObservation) {
			r.Identity.Ownership = metricsmodel.OwnershipNotOwned
		}, contradiction: true},
		{name: "source error with evidence", err: errors.New("raw source error")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := func(ctx context.Context, b ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
				if id != owned(1) {
					t.Fatal("continued sampling after disqualifying a sum")
				}
				r, _ := memory(ctx, b, id)
				if tc.edit != nil {
					tc.edit(&r)
				}
				return r, tc.err
			}
			i := inventory(owned(1), owned(2))
			count := reading(2)
			if tc.contradiction {
				count = metricsmodel.UintReading{}
			}
			checkMeasurements(t, collect(t, collector(t, BoundSources{Host: host, Inventory: i, Memory: m})), true, count, metricsmodel.UintReading{})
			s, err := collector(t, BoundSources{Inventory: i, Memory: m}).Collect(context.Background(), daemonID)
			unavailable(t, s, err)
		})
	}
}

func TestVerifiedZeroAndSamplingAtCap(t *testing.T) {
	ids := make([]metricsmodel.ContainerIdentity, MaxSamples)
	for i := range ids {
		ids[i] = owned(i)
	}
	calls := 0
	c := collector(t, BoundSources{
		Inventory: inventory(ids...),
		Memory: func(_ context.Context, _ ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
			if id != ids[calls] {
				t.Fatalf("unexpected sample %s", id.ID)
			}
			calls++
			return metricsmodel.ContainerMemoryObservation{Identity: id, SourceTime: current(), UsageBytes: reading(0)}, nil
		},
	})
	checkMeasurements(t, collect(t, c), false, reading(MaxSamples), reading(0))
	if calls != MaxSamples {
		t.Fatalf("sample calls = %d", calls)
	}
}

func TestBindingLossInvalidatesIndependentHost(t *testing.T) {
	c := collector(t, BoundSources{
		Host: host, Inventory: inventory(owned(1)),
		Memory: func(context.Context, ReadBudget, metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
			return metricsmodel.ContainerMemoryObservation{}, fmt.Errorf("private cause: %w", ErrDaemonUnverified)
		},
	})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
}

func TestCallDeadlineWithholdsLateSourceButAllowsIndependentEvidence(t *testing.T) {
	c := collector(t, BoundSources{
		Host: func(ctx context.Context, b ReadBudget) (metricsmodel.HostObservation, error) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > CallTimeout {
				t.Fatalf("missing or excessive per-call deadline: %v", deadline)
			}
			<-ctx.Done()
			return host(ctx, b) // Late success must not leak host readings.
		},
		Inventory: inventory(owned(1)), Memory: memory,
	})
	checkMeasurements(t, collect(t, c), false, reading(1), reading(42))
}

func TestOverallDeadlineDiscardsEarlierSuccess(t *testing.T) {
	var hostCalled bool
	c := collector(t, BoundSources{
		Host: func(ctx context.Context, b ReadBudget) (metricsmodel.HostObservation, error) {
			hostCalled = true
			return host(ctx, b)
		},
		Inventory: func(ctx context.Context, _ ReadBudget, _ int) (Inventory, error) {
			<-ctx.Done()
			return Inventory{Exhaustive: true, SourceTime: current()}, nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s, err := c.Collect(ctx, daemonID)
	unavailable(t, s, err)
	if !hostCalled {
		t.Fatal("host wasn't read before deadline")
	}
}

func TestNoncooperativeCallRetainsAdmissionThroughCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		close(release)
		<-done
	}()
	var verifications, invocations, released atomic.Int32
	c := New(func(context.Context, ReadBudget, string) (BoundSources, error) {
		verifications.Add(1)
		return BoundSources{
			Host: func(ctx context.Context, b ReadBudget) (metricsmodel.HostObservation, error) {
				if invocations.Add(1) == 1 {
					close(entered)
					<-release // Intentionally ignores ctx until test cleanup.
				}
				return host(ctx, b)
			},
			Release: func() { released.Add(1) },
		}, nil
	})
	var s metricsmodel.Snapshot
	var err error
	go func() {
		defer close(done)
		s, err = c.Collect(ctx, daemonID)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	cancel()
	for range 100 {
		if busy, err := c.Collect(context.Background(), daemonID); busy != (metricsmodel.Snapshot{}) || err != ErrBusy {
			t.Fatalf("overlap did not refuse immediately: %v (%v)", busy, err)
		}
	}
	select {
	case <-done:
		t.Fatal("returned while upstream work was still running")
	default:
	}
	if verifications.Load() != 1 || invocations.Load() != 1 || released.Load() != 0 {
		t.Fatalf("unexpected overlap work: verifications=%d source invocations=%d releases=%d", verifications.Load(), invocations.Load(), released.Load())
	}
	stopped, canceled := c.Collect(ctx, daemonID)
	unavailable(t, stopped, canceled)
	// Let cleanup unblock the callback and inspect the final result after
	// synchronizing with the caller; no work may be detached by Collect.
	t.Cleanup(func() {
		unavailable(t, s, err)
		if n := released.Load(); n != 1 {
			t.Fatalf("binding released %d times", n)
		}
		checkMeasurements(t, collect(t, c), true, metricsmodel.UintReading{}, metricsmodel.UintReading{})
	})
}

func TestReleaseOnVerificationError(t *testing.T) {
	var releases int
	c := New(func(context.Context, ReadBudget, string) (BoundSources, error) {
		return BoundSources{
			Host: func(context.Context, ReadBudget) (metricsmodel.HostObservation, error) {
				t.Fatal("observed host without verified binding")
				return metricsmodel.HostObservation{}, nil
			},
			Release: func() { releases++ },
		}, errors.New("private verification failure")
	})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
	if releases != 1 {
		t.Fatalf("binding released %d times", releases)
	}
}

func TestMissingOrCanceledEnrollmentDoesNoWork(t *testing.T) {
	c := New(func(context.Context, ReadBudget, string) (BoundSources, error) {
		t.Fatal("called verifier without valid admission")
		return BoundSources{}, nil
	})
	s, err := c.Collect(context.Background(), "")
	unavailable(t, s, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err = c.Collect(ctx, daemonID)
	unavailable(t, s, err)
}
