package brokersecret

import (
	"fmt"
	"io"
	"time"
)

type Review struct {
	Handle    string    `json:"handle"`
	Scope     Scope     `json:"scope"`
	ExpiresAt time.Time `json:"expiresAt"`
	Active    bool      `json:"active"`
}
type Mount struct {
	Service, Name, HostPath, Target string
	UID, GID                        int64
	Mode                            uint32
}
type Materialized struct {
	store      *Store
	review     Review
	mounts     []Mount
	retirement *Retirement
}

// IsRetirement denotes a MAC-verified, explicit empty generation, not an empty ordinary envelope.
func (m *Materialized) IsRetirement() bool { return m != nil && m.retirement != nil }
func (m *Materialized) Previous() []Reference {
	if m == nil || m.retirement == nil {
		return nil
	}
	return append([]Reference(nil), m.retirement.Previous...)
}

func (m *Materialized) Mounts() []Mount {
	if m == nil {
		return nil
	}
	return append([]Mount(nil), m.mounts...)
}
func (Materialized) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (Materialized) Format(w fmt.State, _ rune) {
	_, _ = io.WriteString(w, "SecretMaterialization{redacted}")
}
func (Mount) Format(w fmt.State, _ rune)    { _, _ = io.WriteString(w, "SecretMount{redacted}") }
func (*Store) Format(w fmt.State, _ rune)   { _, _ = io.WriteString(w, "SecretStore{redacted}") }
func (*Store) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
