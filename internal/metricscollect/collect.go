// Package metricscollect coordinates one fresh, bounded metrics acquisition.
// It has no native sources, cache, retry, or background work.
package metricscollect

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/umayr/dill/internal/metricsmodel"
)

// Trial ceilings, not a claim about supported native daemon capacity.
const (
	AcquisitionTimeout = time.Second
	CallTimeout        = 250 * time.Millisecond
	MaxCandidates      = 100
	MaxSamples         = 20
	MaxUpstreamBytes   = 4 << 20
	// MaxUpstreamCalls allows one verification operation, one host open/read,
	// one exhaustive inventory request, and up to 20 one-shot samples in
	// the single-operation best case. Extra version, binding, pagination,
	// or incarnation-proof requests consume the same allowance and reduce
	// what fits. No retry or request outside Do is permitted.
	MaxUpstreamCalls = 3 + MaxSamples
)

var (
	// ErrBusy is a non-queued admission refusal. The transport should also
	// enforce its own admission and minimum interval before invoking Collect.
	ErrBusy = errors.New("metrics acquisition busy")
	// ErrUnavailable means no snapshot may be published. Source errors never
	// escape Collect.
	ErrUnavailable = errors.New("metrics acquisition unavailable")
	// ErrDaemonUnverified is for a bound source to return if it loses the
	// enrolled daemon binding. It invalidates the whole acquisition, not just
	// that source. Ordinary source failures only withhold affected evidence.
	ErrDaemonUnverified = errors.New("metrics daemon binding unverified")
)

// VerifyDaemon must establish a fresh binding to the enrolled daemon for
// this acquisition. Matching the expected ID or a socket path alone is not
// proof. A nil error attests the binding; all returned functions must use it
// (or fail with ErrDaemonUnverified if it cannot be maintained). Verification
// follows the same cancellation, budget, and no-background-work rules as the
// bound operations. It may return a Release hook even on verification failure.
type VerifyDaemon func(ctx context.Context, budget ReadBudget, expectedDaemonID string) (BoundSources, error)

// BoundSources are trusted synchronous operations. Unqualified sources must
// be nil or return no evidence. Operations must honor context cancellation,
// stop their I/O and close response bodies before returning, use the budget
// for every upstream body/local read (even on failure), and bound their own
// parsing and native request counts. They must not retry, leave work in
// flight, return cached evidence, or use a request timestamp as proof of a
// reading's age. A source must require a complete body before attesting data.
//
// Inventory must stop at maxCandidates, attesting exhaustion only when the
// covered population is proven complete. Memory must prove the returned
// identity, not copy it from the request. Host must prove locality to the
// bound daemon and that both readings came from the same native read.
type BoundSources struct {
	Host      HostSource
	Inventory func(ctx context.Context, budget ReadBudget, maxCandidates int) (Inventory, error)
	Memory    func(ctx context.Context, budget ReadBudget, candidate metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error)
	// Release synchronously releases any retained binding resources (e.g. a
	// pinned connection). It must not initiate requests or block on the
	// daemon. It runs once before admission is released, even after timeout
	// or verification error, if the verifier returned it.
	Release func()
}

// Inventory contains fresh classification for all candidates, including
// those not eligible for sampling. Exhaustive is not implied by len(Candidates)
// being within bounds, or even by an empty result. Candidates are transferred
// to the caller and must not be mutated after return.
type Inventory struct {
	Exhaustive bool
	SourceTime metricsmodel.SourceTime
	Candidates []metricsmodel.ContainerIdentity
}

// Collector admits at most one acquisition at a time without a queue. Use a
// single instance per agent. A Collector must not be copied after first use.
type Collector struct {
	verify VerifyDaemon
	active atomic.Bool
}

func New(verify VerifyDaemon) *Collector { return &Collector{verify: verify} }

// Collect returns only a usable immutable snapshot or a generic error. There
// is no renderer or transport dependency. expectedDaemonID must come from
// trusted enrollment configuration, never an HTTP request or the daemon's
// self-report. An already canceled context returns ErrUnavailable, not ErrBusy.
// Sources run serially (at most 23 invocations, one in flight). Deadlines
// disqualify late results but cannot interrupt a noncooperative callback:
// Collect waits without spawning goroutines or releasing admission early.
func (c *Collector) Collect(ctx context.Context, expectedDaemonID string) (metricsmodel.Snapshot, error) {
	unavailable := metricsmodel.Snapshot{}
	if c == nil || c.verify == nil || expectedDaemonID == "" || ctx.Err() != nil {
		return unavailable, ErrUnavailable
	}
	if !c.active.CompareAndSwap(false, true) {
		return unavailable, ErrBusy
	}
	defer c.active.Store(false)

	started := time.Now()
	ctx, cancel := context.WithDeadline(ctx, started.Add(AcquisitionTimeout))
	defer cancel()
	a := acquisition{ctx: ctx, started: started, upstream: upstreamBudget{
		bytesLeft: MaxUpstreamBytes, callsLeft: MaxUpstreamCalls,
	}}
	o := a.observe(c.verify, expectedDaemonID)
	o.EndedAt = time.Now()
	o.AcquisitionComplete = a.live(o.EndedAt)
	snapshot := metricsmodel.Compute(o)
	if !a.live(time.Now()) || !snapshot.Usable() {
		return unavailable, ErrUnavailable
	}
	return snapshot, nil
}

func (a *acquisition) observe(verify VerifyDaemon, expectedDaemonID string) metricsmodel.Observation {
	sources, _, ok := call(a, func(ctx context.Context, budget ReadBudget) (BoundSources, error) {
		return verify(ctx, budget, expectedDaemonID)
	})
	if sources.Release != nil {
		defer sources.Release()
	}
	if !ok {
		return metricsmodel.Observation{}
	}

	o := metricsmodel.Observation{DaemonIdentityVerified: true, StartedAt: a.started}
	if sources.Host != nil {
		host, ended, ok := call(a, sources.Host)
		if ok && a.current(host.SourceTime, ended) {
			o.Host = host
		}
	}
	if sources.Inventory != nil {
		o.Containers = a.containers(sources)
	}
	return o
}

type acquisition struct {
	ctx         context.Context
	started     time.Time
	upstream    upstreamBudget
	bindingLost bool
}

func (a *acquisition) live(now time.Time) bool {
	return a.upstream.breach == nil && !a.bindingLost && beforeDeadline(a.ctx, now)
}

func beforeDeadline(ctx context.Context, now time.Time) bool {
	deadline, _ := ctx.Deadline() // Both acquisition and call contexts have deadlines.
	return ctx.Err() == nil && now.Before(deadline)
}

func (a *acquisition) current(t metricsmodel.SourceTime, ended time.Time) bool {
	return t.Verified && !t.At.IsZero() && !t.At.Before(a.started) && !t.At.After(ended)
}

// Call timeout withholds the operation's evidence even if the operation
// reports success. Only overall timeout, lost daemon binding, and breach of
// either shared upstream budget invalidate all sources. A failed invocation's value
// is returned only so observe can release a verifier's resources.
func call[T any](a *acquisition, fn func(context.Context, ReadBudget) (T, error)) (T, time.Time, bool) {
	var zero T
	started := time.Now()
	if !a.live(started) {
		return zero, started, false
	}
	ctx, cancel := context.WithDeadline(a.ctx, started.Add(CallTimeout))
	defer cancel()
	if !beforeDeadline(ctx, time.Now()) {
		return zero, started, false
	}
	v, err := fn(ctx, ReadBudget{ctx: ctx, upstream: &a.upstream})
	ended := time.Now()
	if errors.Is(err, ErrDaemonUnverified) {
		a.bindingLost = true
	}
	if err != nil || !beforeDeadline(ctx, ended) || !a.live(ended) {
		return v, ended, false
	}
	return v, ended, true
}

func (a *acquisition) containers(sources BoundSources) metricsmodel.ContainerObservation {
	list, ended, ok := call(a, func(ctx context.Context, budget ReadBudget) (Inventory, error) {
		return sources.Inventory(ctx, budget, MaxCandidates)
	})
	if !ok || !list.Exhaustive || len(list.Candidates) > MaxCandidates || !a.current(list.SourceTime, ended) {
		return metricsmodel.ContainerObservation{}
	}

	// Validate the whole set before requesting any samples. Do not sample a
	// prefix of an unclassifiable or oversized population.
	out := metricsmodel.ContainerObservation{Exhaustive: true, SourceTime: list.SourceTime}
	out.Candidates = make([]metricsmodel.ContainerCandidate, len(list.Candidates))
	seen := make(map[string]struct{}, len(list.Candidates))
	eligible := 0
	missingIncarnation := false
	for i, id := range list.Candidates {
		if _, duplicate := seen[id.ID]; duplicate || id.ID == "" {
			return metricsmodel.ContainerObservation{}
		}
		seen[id.ID] = struct{}{}
		out.Candidates[i].Identity = id
		switch id.State {
		case metricsmodel.StateStopped:
			continue
		case metricsmodel.StateRunning:
		default:
			return metricsmodel.ContainerObservation{}
		}
		switch id.Ownership {
		case metricsmodel.OwnershipNotOwned:
		case metricsmodel.OwnershipDill:
			eligible++
			missingIncarnation = missingIncarnation || id.Incarnation == ""
		default:
			return metricsmodel.ContainerObservation{}
		}
	}
	if eligible > MaxSamples || missingIncarnation || sources.Memory == nil {
		return out // Count remains known; the whole set cannot be measured.
	}
	for i, row := range out.Candidates {
		id := row.Identity
		if id.State != metricsmodel.StateRunning || id.Ownership != metricsmodel.OwnershipDill {
			continue
		}
		r, ended, ok := call(a, func(ctx context.Context, budget ReadBudget) (metricsmodel.ContainerMemoryObservation, error) {
			return sources.Memory(ctx, budget, id)
		})
		if !ok || !a.current(r.SourceTime, ended) {
			break // No sum is possible; don't issue further upstream work.
		}
		// Preserve fresh contradictions for the model to invalidate, not
		// just drop the sample as if the inventory were still trustworthy.
		out.Candidates[i].Memory = r
		if r.Identity != id || !r.UsageBytes.Present {
			break
		}
	}
	return out
}
