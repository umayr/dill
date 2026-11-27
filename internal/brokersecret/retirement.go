package brokersecret

import (
	"encoding/json"
	"io"
)

// Retirement is an explicit metadata-only request for a new empty generation.
// It never permits an empty ordinary secret Envelope and never authorizes
// deletion/GC or engine mutation. Target-side authenticated live inventory must
// additionally cover every still-running predecessor workload before review.
const RetirementVersion = "dill.broker-retirement/1"

type Retirement struct {
	Contract string      `json:"contract"`
	Scope    Scope       `json:"scope"`
	Previous []Reference `json:"previous"`
}

func sameRuntimeGroup(a, b Scope) bool {
	return a.Registration == b.Registration && a.Stack == b.Stack && a.Engine == b.Engine && a.TargetDigest == b.TargetDigest && a.EndpointDigest == b.EndpointDigest
}
func (r Retirement) Valid() bool {
	if r.Contract != RetirementVersion || !r.Scope.Valid() || len(r.Previous) == 0 || len(r.Previous) > MaxEntries {
		return false
	}
	handles := map[string]bool{}
	generations := map[string]bool{}
	for _, p := range r.Previous {
		if !p.Valid() || !sameRuntimeGroup(p.Scope, r.Scope) || p.Scope.Generation == r.Scope.Generation || p.Scope.BindingRevision > r.Scope.BindingRevision || handles[p.Handle] || generations[p.Scope.Generation] {
			return false
		}
		handles[p.Handle] = true
		generations[p.Scope.Generation] = true
	}
	return true
}
func DecodeRetirement(in io.Reader) (Retirement, error) {
	var r Retirement
	if in == nil {
		return r, ErrInvalid
	}
	if DecodeMetadata(in, &r) != nil || !r.Valid() {
		return Retirement{}, ErrInvalid
	}
	return r, nil
}
func EncodeRetirement(r Retirement) ([]byte, error) {
	if !r.Valid() {
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxMetadata {
		return nil, ErrInvalid
	}
	return raw, nil
}
