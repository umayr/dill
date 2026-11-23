package metricsmodel_test

import (
	"math"
	"testing"
	"time"

	m "github.com/umayr/dill/internal/metricsmodel"
)

const exactLimit uint64 = 1<<53 - 1

func measured(n uint64) m.UintReading { return m.UintReading{Value: n, Present: true} }

func observation() m.Observation {
	start := time.Unix(100, 0)
	return m.Observation{
		DaemonIdentityVerified: true,
		AcquisitionComplete:    true,
		StartedAt:              start,
		EndedAt:                start.Add(time.Second),
		Containers: m.ContainerObservation{
			Exhaustive: true,
			SourceTime: m.SourceTime{At: start, Verified: true},
		},
	}
}

func withHost(o m.Observation) m.Observation {
	o.Host = m.HostObservation{
		LocalityVerified: true,
		SameRead:         true,
		SourceTime:       m.SourceTime{At: o.StartedAt, Verified: true},
		TotalKB:          measured(128),
		AvailableKB:      measured(0),
	}
	return o
}

func candidate(o m.Observation, id string, bytes uint64) m.ContainerCandidate {
	identity := m.ContainerIdentity{ID: id, Incarnation: "first", State: m.StateRunning, Ownership: m.OwnershipDill}
	return m.ContainerCandidate{
		Identity: identity,
		Memory: m.ContainerMemoryObservation{
			Identity:   identity,
			SourceTime: m.SourceTime{At: o.EndedAt, Verified: true},
			UsageBytes: measured(bytes),
		},
	}
}

type expected struct {
	total, available uint64
	hostOK           bool
	usage, count     m.UintReading
	usable           bool
}

func assertSnapshot(t *testing.T, s m.Snapshot, want expected) {
	t.Helper()
	if total, avail, ok := s.HostMemoryBytes(); total != want.total || avail != want.available || ok != want.hostOK {
		t.Errorf("HostMemoryBytes() = (%d, %d, %t); want (%d, %d, %t)", total, avail, ok, want.total, want.available, want.hostOK)
	}
	if n, ok := s.OwnedMemoryUsageBytes(); (m.UintReading{Value: n, Present: ok}) != want.usage {
		t.Errorf("OwnedMemoryUsageBytes() = (%d, %t); want (%d, %t)", n, ok, want.usage.Value, want.usage.Present)
	}
	if n, ok := s.OwnedRunningContainers(); (m.UintReading{Value: n, Present: ok}) != want.count {
		t.Errorf("OwnedRunningContainers() = (%d, %t); want (%d, %t)", n, ok, want.count.Value, want.count.Present)
	}
	if got := s.Usable(); got != want.usable {
		t.Errorf("Usable() = %t; want %t", got, want.usable)
	}
}

func TestAcquisitionPrerequisites(t *testing.T) {
	assertSnapshot(t, m.Snapshot{}, expected{})
	assertSnapshot(t, m.Compute(m.Observation{}), expected{})

	for _, tc := range []struct {
		name string
		edit func(*m.Observation)
	}{
		{"unverified daemon", func(o *m.Observation) { o.DaemonIdentityVerified = false }},
		{"unsafe acquisition", func(o *m.Observation) { o.AcquisitionComplete = false }},
		{"missing start", func(o *m.Observation) { o.StartedAt = time.Time{} }},
		{"missing end", func(o *m.Observation) { o.EndedAt = time.Time{} }},
		{"reversed window", func(o *m.Observation) { o.StartedAt, o.EndedAt = o.EndedAt, o.StartedAt }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := withHost(observation())
			o.Containers.Candidates = []m.ContainerCandidate{candidate(o, "print-nothing", 12)}
			tc.edit(&o)
			assertSnapshot(t, m.Compute(o), expected{})
		})
	}
}

func TestHostPair(t *testing.T) {
	const largestKB = exactLimit / 1024
	for _, tc := range []struct {
		name             string
		edit             func(*m.Observation)
		total, available uint64
		ok               bool
	}{
		{name: "same read with measured zero available", total: 128 * 1024, ok: true},
		{name: "largest exactly representable kB pair", edit: func(o *m.Observation) {
			o.Host.TotalKB, o.Host.AvailableKB = measured(largestKB), measured(largestKB)
		}, total: largestKB * 1024, available: largestKB * 1024, ok: true},
		{name: "next kB exceeds sample limit", edit: func(o *m.Observation) { o.Host.TotalKB = measured(largestKB + 1) }},
		{name: "multiplication would wrap to zero", edit: func(o *m.Observation) { o.Host.TotalKB = measured(1 << 54) }},
		{name: "maximum uint64", edit: func(o *m.Observation) { o.Host.TotalKB = measured(math.MaxUint64) }},
		{name: "missing total", edit: func(o *m.Observation) { o.Host.TotalKB.Present = false }},
		{name: "missing available", edit: func(o *m.Observation) { o.Host.AvailableKB.Present = false }},
		{name: "zero total invalid on a native host", edit: func(o *m.Observation) { o.Host.TotalKB = measured(0) }},
		{name: "available exceeds total", edit: func(o *m.Observation) { o.Host.AvailableKB = measured(129) }},
		{name: "different reads", edit: func(o *m.Observation) { o.Host.SameRead = false }},
		{name: "locality unproven", edit: func(o *m.Observation) { o.Host.LocalityVerified = false }},
		{name: "age unverified", edit: func(o *m.Observation) { o.Host.SourceTime.Verified = false }},
		{name: "missing source time", edit: func(o *m.Observation) { o.Host.SourceTime.At = time.Time{} }},
		{name: "stale source", edit: func(o *m.Observation) { o.Host.SourceTime.At = o.StartedAt.Add(-time.Nanosecond) }},
		{name: "future source", edit: func(o *m.Observation) { o.Host.SourceTime.At = o.EndedAt.Add(time.Nanosecond) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := withHost(observation())
			if tc.edit != nil {
				tc.edit(&o)
			}
			assertSnapshot(t, m.Compute(o), expected{total: tc.total, available: tc.available, hostOK: tc.ok, count: measured(0), usable: tc.ok})
		})
	}
}

func TestExhaustivePopulation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*m.Observation)
		count m.UintReading
		usage m.UintReading
	}{
		{name: "exhaustive empty", edit: func(o *m.Observation) { o.Containers.Candidates = nil }, count: measured(0)},
		{name: "stopped and unowned excluded", count: measured(2), usage: measured(30)},
		{name: "exclusions only", edit: func(o *m.Observation) { o.Containers.Candidates = o.Containers.Candidates[2:] }, count: measured(0)},
		{name: "truncated with valid rows", edit: func(o *m.Observation) { o.Containers.Exhaustive = false }},
		{name: "truncated with no rows", edit: func(o *m.Observation) { o.Containers = m.ContainerObservation{} }},
		{name: "inventory freshness unverified", edit: func(o *m.Observation) { o.Containers.SourceTime.Verified = false }},
		{name: "stale inventory despite current samples", edit: func(o *m.Observation) {
			o.Containers.SourceTime.At = o.StartedAt.Add(-time.Nanosecond)
		}},
		{name: "unknown ownership after missing reading", edit: func(o *m.Observation) {
			o.Containers.Candidates[0].Memory = m.ContainerMemoryObservation{}
			o.Containers.Candidates[1].Identity.Ownership = m.OwnershipUnknown
		}},
		{name: "invalid ownership enum", edit: func(o *m.Observation) { o.Containers.Candidates[0].Identity.Ownership = 255 }},
		{name: "unknown running state", edit: func(o *m.Observation) { o.Containers.Candidates[0].Identity.State = m.StateUnknown }},
		{name: "invalid state enum", edit: func(o *m.Observation) { o.Containers.Candidates[0].Identity.State = 255 }},
		{name: "missing ID", edit: func(o *m.Observation) { o.Containers.Candidates[0].Identity.ID = "" }},
		{name: "duplicate ID across classifications and incarnations", edit: func(o *m.Observation) {
			o.Containers.Candidates[2].Identity.ID = o.Containers.Candidates[0].Identity.ID
			o.Containers.Candidates[2].Identity.Incarnation = "second"
		}},
		{name: "same-incarnation ownership contradiction for included row", edit: func(o *m.Observation) {
			o.Containers.Candidates[0].Memory.Identity.Ownership = m.OwnershipNotOwned
		}},
		{name: "same-incarnation ownership contradiction for excluded running row", edit: func(o *m.Observation) {
			x := &o.Containers.Candidates[3]
			x.Memory = candidate(*o, x.Identity.ID, 100).Memory
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := withHost(observation())
			stopped := candidate(o, "c", 999)
			stopped.Identity.State, stopped.Identity.Ownership = m.StateStopped, m.OwnershipUnknown
			unowned := candidate(o, "d", 999)
			unowned.Identity.Ownership = m.OwnershipNotOwned
			unowned.Memory = m.ContainerMemoryObservation{}
			o.Containers.Candidates = []m.ContainerCandidate{candidate(o, "a", 10), candidate(o, "b", 20), stopped, unowned}
			if tc.edit != nil {
				tc.edit(&o)
			}
			assertSnapshot(t, m.Compute(o), expected{total: 128 * 1024, hostOK: true, usage: tc.usage, count: tc.count, usable: true})
		})
	}
}

func TestUnqualifiedMemoryCanRetainCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*m.Observation, *m.ContainerCandidate)
	}{
		{"missing memory", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory = m.ContainerMemoryObservation{} }},
		{"missing usage field", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.UsageBytes.Present = false }},
		{"wrong container ID", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.Identity.ID = "other" }},
		{"changed incarnation", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.Identity.Incarnation = "second" }},
		{"missing inventory incarnation", func(_ *m.Observation, x *m.ContainerCandidate) { x.Identity.Incarnation = "" }},
		{"missing sample incarnation", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.Identity.Incarnation = "" }},
		{"sample no longer running", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.Identity.State = m.StateStopped }},
		{"sample ownership not proven", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.Identity.Ownership = m.OwnershipUnknown }},
		{"sample age unverified", func(_ *m.Observation, x *m.ContainerCandidate) { x.Memory.SourceTime.Verified = false }},
		{"stale sample", func(o *m.Observation, x *m.ContainerCandidate) {
			x.Memory.SourceTime.At = o.StartedAt.Add(-time.Nanosecond)
		}},
		{"future sample", func(o *m.Observation, x *m.ContainerCandidate) {
			x.Memory.SourceTime.At = o.EndedAt.Add(time.Nanosecond)
		}},
		{"previous acquisition ownership is not a contradiction", func(o *m.Observation, x *m.ContainerCandidate) {
			x.Memory.SourceTime.At = o.StartedAt.Add(-time.Nanosecond)
			x.Memory.Identity.Ownership = m.OwnershipNotOwned
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := observation()
			o.Containers.Candidates = []m.ContainerCandidate{candidate(o, "a", 0), candidate(o, "b", 10)}
			tc.edit(&o, &o.Containers.Candidates[0])
			assertSnapshot(t, m.Compute(o), expected{count: measured(2)}) // Count alone is 503.
			assertSnapshot(t, m.Compute(withHost(o)), expected{total: 128 * 1024, hostOK: true, count: measured(2), usable: true})
		})
	}
}

func TestAggregateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		values    []uint64
		wantUsage m.UintReading
	}{
		{"empty set cannot publish zero", nil, m.UintReading{}},
		{"exact zero with a nonempty set", []uint64{0, 0}, measured(0)},
		{"max exact sample", []uint64{exactLimit}, measured(exactLimit)},
		{"max exact sum", []uint64{exactLimit - 1, 1, 0}, measured(exactLimit)},
		{"max plus one reading", []uint64{exactLimit + 1}, m.UintReading{}},
		{"individually valid summands exceed limit", []uint64{exactLimit, 1}, m.UintReading{}},
		{"sum cannot wrap to zero", []uint64{1, math.MaxUint64}, m.UintReading{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := observation()
			for i, v := range tc.values {
				o.Containers.Candidates = append(o.Containers.Candidates, candidate(o, string(rune('a'+i)), v))
			}
			assertSnapshot(t, m.Compute(o), expected{usage: tc.wantUsage, count: measured(uint64(len(tc.values))), usable: tc.wantUsage.Present})
		})
	}
}

func TestSnapshotsRetainNeitherInputsNorPreviousScrapes(t *testing.T) {
	o := withHost(observation())
	o.Containers.Candidates = []m.ContainerCandidate{candidate(o, "a", 10)}
	first := m.Compute(o)
	// Independently clearing host evidence and mutating the caller's candidate
	// slice cannot change the old snapshot or leave either measurement cached.
	o.Host = m.HostObservation{}
	o.Containers.Candidates[0].Memory = m.ContainerMemoryObservation{}
	assertSnapshot(t, first, expected{total: 128 * 1024, hostOK: true, usage: measured(10), count: measured(1), usable: true})
	assertSnapshot(t, m.Compute(o), expected{count: measured(1)})
}
