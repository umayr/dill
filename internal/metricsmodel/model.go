// Package metricsmodel validates synthetic or caller-supplied evidence for a
// single scrape. It performs no acquisition and retains no source identities,
// timestamps, or references to the evidence in the resulting snapshot.
package metricsmodel

import "time"

// maxSample is the inclusive v1 limit for exact Prometheus integer samples.
const maxSample uint64 = 1<<53 - 1

// UintReading distinguishes a missing field from a measured zero.
type UintReading struct {
	Value   uint64
	Present bool
}

// SourceTime is evidence of when a reading was produced, not when it was
// fetched. Verified asserts the caller established credible source age and
// related At to the acquisition window's clock.
type SourceTime struct {
	At       time.Time
	Verified bool
}

// Observation is input evidence for Compute, not a validated snapshot. All
// fields must describe one acquisition. The caller must not modify Candidates
// concurrently with Compute; Compute does not retain it.
type Observation struct {
	// DaemonIdentityVerified attests that this acquisition is bound to the
	// enrolled daemon, not merely that its endpoint or daemon ID matched.
	DaemonIdentityVerified bool
	// AcquisitionComplete attests the acquisition reached a safe conclusion
	// within its enforced bounds. This does not assert that either source
	// succeeded (e.g. an individual call may have returned no reading).
	AcquisitionComplete bool
	// StartedAt and EndedAt delimit the acquisition window, inclusively.
	StartedAt, EndedAt time.Time
	Host               HostObservation
	Containers         ContainerObservation
}

// HostObservation holds the native Linux MemTotal and MemAvailable fields in
// the kernel's kB units (1 kB = 1024 bytes). Both must come from one read of the
// proven local host; daemon identity by itself is not proof of locality.
// A valid pair has a positive total and available no greater than total.
type HostObservation struct {
	LocalityVerified     bool
	SameRead             bool
	SourceTime           SourceTime
	TotalKB, AvailableKB UintReading
}

// ContainerObservation is a single inventory from the verified daemon.
// Exhaustive must only be set when the enumeration was not truncated or
// otherwise incomplete. Include each candidate once, even if not sampled.
// SourceTime is when the inventory was observed or assembled; mark it
// verified only when enumeration and classification evidence are current
// for this acquisition, including when the inventory is empty.
type ContainerObservation struct {
	Exhaustive bool
	SourceTime SourceTime
	Candidates []ContainerCandidate
}

// ContainerState is verified eligibility with respect to running status.
type ContainerState uint8

const (
	StateUnknown ContainerState = iota
	StateStopped
	StateRunning
)

// Ownership distinguishes an explicit classification from unverified ownership.
type Ownership uint8

const (
	OwnershipUnknown Ownership = iota
	OwnershipNotOwned
	OwnershipDill
)

// ContainerIdentity describes proven classification at the time of observation.
// Use unknown values when state or ownership is not verified. ID is the stable
// full container identity for deduplication; Incarnation distinguishes restarts
// of the same container and is required to link a reading to the inventory.
// These are opaque caller-supplied keys in the verified daemon's namespace.
type ContainerIdentity struct {
	ID          string
	Incarnation string
	State       ContainerState
	Ownership   Ownership
}

// ContainerCandidate binds inventory classification and optional sample
// evidence in one row, so the count and sum use the same population.
type ContainerCandidate struct {
	Identity ContainerIdentity
	Memory   ContainerMemoryObservation
}

// ContainerMemoryObservation holds raw Docker memory_stats.usage, not working
// set. Identity must describe the reading's verified identity and status, not
// merely what was requested from the daemon. Leave proof absent when source
// age or incarnation cannot be established across sampling.
type ContainerMemoryObservation struct {
	Identity   ContainerIdentity
	SourceTime SourceTime
	UsageBytes UintReading
}

// Snapshot is the immutable, validated result of one acquisition. Its zero
// value has no usable measurements. A renderer must check Usable before
// publishing anything, including any known auxiliary count or status flags.
type Snapshot struct {
	hostTotal, hostAvailable uint64
	hostOK                   bool
	ownedMemory, ownedCount  UintReading
}

// Usable reports whether the snapshot qualifies for 200 rather than 503 with
// no metrics. A count alone is not a usable measurement.
func (s Snapshot) Usable() bool { return s.hostOK || s.ownedMemory.Present }

// HostMemoryBytes returns the host pair together or neither. Its presence bit
// is also the value of the host collection-complete flag.
func (s Snapshot) HostMemoryBytes() (total, available uint64, ok bool) {
	return s.hostTotal, s.hostAvailable, s.hostOK
}

// OwnedMemoryUsageBytes returns a sum only for a fully measured nonempty set.
// Its presence bit is also the owned memory collection-complete flag.
func (s Snapshot) OwnedMemoryUsageBytes() (uint64, bool) {
	return s.ownedMemory.Value, s.ownedMemory.Present
}

// OwnedRunningContainers returns the known count, even when memory is missing.
func (s Snapshot) OwnedRunningContainers() (uint64, bool) {
	return s.ownedCount.Value, s.ownedCount.Present
}

// Compute validates available evidence and preserves independent measurements
// when a source is missing or invalid. Unverified acquisition-wide prerequisites
// invalidate all sources. It does not access the system clock.
func Compute(o Observation) Snapshot {
	if !o.DaemonIdentityVerified || !o.AcquisitionComplete ||
		o.StartedAt.IsZero() || o.EndedAt.IsZero() || o.EndedAt.Before(o.StartedAt) {
		return Snapshot{}
	}

	var s Snapshot
	s.hostTotal, s.hostAvailable, s.hostOK = o.hostMemory()
	s.ownedCount, s.ownedMemory = o.ownedContainers()
	return s
}

func (o Observation) current(t SourceTime) bool {
	return t.Verified && !t.At.IsZero() && !t.At.Before(o.StartedAt) && !t.At.After(o.EndedAt)
}

func (o Observation) hostMemory() (uint64, uint64, bool) {
	h := o.Host
	if !h.LocalityVerified || !h.SameRead || !o.current(h.SourceTime) ||
		!h.TotalKB.Present || !h.AvailableKB.Present ||
		h.TotalKB.Value == 0 || h.AvailableKB.Value > h.TotalKB.Value {
		return 0, 0, false
	}

	total, totalOK := kbToSample(h.TotalKB.Value)
	available, availableOK := kbToSample(h.AvailableKB.Value)
	if !totalOK || !availableOK {
		return 0, 0, false
	}
	return total, available, true
}

func kbToSample(kb uint64) (uint64, bool) {
	const bytesPerKB = 1024
	// This stricter limit guards both integer multiplication and conversion
	// to an exactly representable v1 sample.
	if kb > maxSample/bytesPerKB {
		return 0, false
	}
	return kb * bytesPerKB, true
}

func (o Observation) ownedContainers() (count, memory UintReading) {
	inventory := o.Containers
	if !inventory.Exhaustive || !o.current(inventory.SourceTime) {
		return UintReading{}, UintReading{}
	}

	seen := make(map[string]struct{}, len(inventory.Candidates))
	var n, sum uint64
	complete := true
	for _, row := range inventory.Candidates {
		id := row.Identity
		if _, duplicate := seen[id.ID]; id.ID == "" || duplicate {
			return UintReading{}, UintReading{}
		}
		seen[id.ID] = struct{}{}

		switch id.State {
		case StateStopped:
			continue // Proven stopped, so ownership is irrelevant to exclusion.
		case StateRunning:
		default:
			return UintReading{}, UintReading{}
		}
		if !knownOwnership(id.Ownership) {
			return UintReading{}, UintReading{}
		}

		r := row.Memory
		sameIncarnation := id.Incarnation != "" && r.Identity.ID == id.ID && r.Identity.Incarnation == id.Incarnation
		current := o.current(r.SourceTime)
		if sameIncarnation && current && knownOwnership(r.Identity.Ownership) && r.Identity.Ownership != id.Ownership {
			// Contradictory verified ownership for this same incarnation
			// means even the inventory's classification is ambiguous.
			return UintReading{}, UintReading{}
		}
		if id.Ownership == OwnershipNotOwned {
			continue
		}

		n++ // Bounded by the length of the candidate slice (an int).
		if !sameIncarnation || !current || r.Identity.State != StateRunning ||
			r.Identity.Ownership != OwnershipDill || !r.UsageBytes.Present ||
			r.UsageBytes.Value > maxSample-sum {
			complete = false
			continue // Still validate the rest of the inventory for the count.
		}
		sum += r.UsageBytes.Value
	}
	if n > maxSample {
		return UintReading{}, UintReading{}
	}

	count = UintReading{Value: n, Present: true}
	if n > 0 && complete {
		memory = UintReading{Value: sum, Present: true}
	}
	return count, memory
}

func knownOwnership(owner Ownership) bool {
	return owner == OwnershipNotOwned || owner == OwnershipDill
}
