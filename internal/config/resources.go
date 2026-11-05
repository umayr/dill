package config

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// ResourceLimits is the normalized resource configuration for a service.
// Zero values mean "unset"; PidsLimit -1 means unlimited.
type ResourceLimits struct {
	Memory            int64 `json:"memory,omitempty"`
	MemoryReservation int64 `json:"memoryReservation,omitempty"`
	NanoCPUs          int64 `json:"nanoCpus,omitempty"`
	CPUShares         int64 `json:"cpuShares,omitempty"`
	PidsLimit         int64 `json:"pidsLimit,omitempty"`
}

// IsZero reports whether no limit is configured.
func (r ResourceLimits) IsZero() bool {
	return r.Memory == 0 && r.MemoryReservation == 0 && r.NanoCPUs == 0 &&
		r.CPUShares == 0 && r.PidsLimit == 0
}

// NormalizeResources parses the Compose-style resource fields into engine units.
func NormalizeResources(svc *Service) (ResourceLimits, error) {
	var r ResourceLimits

	if svc.MemLimit != "" {
		n, err := ParseBytes(svc.MemLimit)
		if err != nil {
			return r, fmt.Errorf("memLimit: %w", err)
		}
		r.Memory = n
	}
	if svc.MemReservation != "" {
		n, err := ParseBytes(svc.MemReservation)
		if err != nil {
			return r, fmt.Errorf("memReservation: %w", err)
		}
		r.MemoryReservation = n
	}
	if svc.CPUs != "" {
		n, err := parseScaledDecimal(strings.TrimSpace(svc.CPUs), 1_000_000_000)
		if err != nil {
			return r, fmt.Errorf("cpus %q: %w", svc.CPUs, err)
		}
		// Both engines enforce CPU limits using a 100000-microsecond
		// period and an integer quota. Normalize to that shared precision
		// before hashing, creating containers, or comparing live state.
		r.NanoCPUs = n / 10_000 * 10_000
		if r.NanoCPUs == 0 {
			return r, fmt.Errorf("cpus %q must be at least 0.00001", svc.CPUs)
		}
	}
	if svc.CPUShares != nil {
		if *svc.CPUShares < 0 {
			return r, fmt.Errorf("cpuShares must be >= 0")
		}
		r.CPUShares = int64(*svc.CPUShares)
	}
	if svc.PidsLimit != nil {
		if *svc.PidsLimit < -1 {
			return r, fmt.Errorf("pidsLimit must be >= -1")
		}
		r.PidsLimit = int64(*svc.PidsLimit)
	}
	return r, nil
}

// ParseBytes parses a Docker-style size string (512m, 1g, 1.5g, 1024, 1KiB).
func ParseBytes(s string) (int64, error) {
	raw := strings.TrimSpace(strings.ToLower(s))
	if raw == "" {
		return 0, fmt.Errorf("empty size")
	}

	mult := int64(1)
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40},
		{"kb", 1 << 10}, {"mb", 1 << 20}, {"gb", 1 << 30}, {"tb", 1 << 40},
		{"ki", 1 << 10}, {"mi", 1 << 20}, {"gi", 1 << 30}, {"ti", 1 << 40},
		{"k", 1 << 10}, {"m", 1 << 20}, {"g", 1 << 30}, {"t", 1 << 40},
		{"b", 1},
	}
	num := raw
	for _, sf := range suffixes {
		if strings.HasSuffix(raw, sf.suffix) {
			num = strings.TrimSpace(strings.TrimSuffix(raw, sf.suffix))
			mult = sf.mult
			break
		}
	}
	if num == "" {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := parseScaledDecimal(num, mult)
	if err != nil {
		return 0, fmt.Errorf("size %q: %w", s, err)
	}
	return n, nil
}

// Resource quantities use decimal notation, not fractions, hexadecimal, or
// floating-point special values. Parse exactly so large integers and decimal
// fractions cannot round across the int64 boundary.
var resourceDecimal = regexp.MustCompile(`^\+?([0-9]+(\.[0-9]*)?|\.[0-9]+)$`)

func parseScaledDecimal(s string, multiplier int64) (int64, error) {
	if !resourceDecimal.MatchString(s) {
		return 0, fmt.Errorf("must be a non-negative decimal number")
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("invalid decimal number")
	}
	v.Mul(v, new(big.Rat).SetInt64(multiplier))
	if v.Cmp(new(big.Rat).SetInt64(1<<63-1)) > 0 {
		return 0, fmt.Errorf("overflows int64")
	}
	n := new(big.Int).Quo(v.Num(), v.Denom()).Int64()
	if v.Sign() > 0 && n == 0 {
		return 0, fmt.Errorf("positive quantity is smaller than one unit")
	}
	return n, nil
}
