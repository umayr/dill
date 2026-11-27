//go:build linux

package brokersecret

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

// PrepareRetirement creates metadata only; no existing generation is removed or
// deactivated. This authenticates predecessors, NOT live coverage or permission
// to mutate. The qualified planner must still reject omitted secret workloads.
func (s *Store) PrepareRetirement(r Retirement) (Review, error) {
	if !r.Valid() {
		return Review{}, ErrInvalid
	}
	if s == nil {
		return Review{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready() {
		return Review{}, ErrUnavailable
	}
	for _, p := range r.Previous {
		m, dir, err := s.load(p.Handle, p.Scope)
		if err != nil {
			return Review{}, err
		}
		dir.Close()
		if !m.Review.Active {
			return Review{}, ErrUnavailable
		}
	}
	if err := s.capacity(); err != nil {
		return Review{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Review{}, ErrUnavailable
	}
	id := hex.EncodeToString(nonce[:])
	if s.root.Mkdir(id, 0700) != nil {
		return Review{}, ErrUnavailable
	}
	dir, err := s.root.OpenRoot(id)
	if err != nil {
		return Review{}, ErrUnavailable
	}
	defer dir.Close()
	now := s.now().UTC()
	copy := r
	copy.Previous = append([]Reference(nil), r.Previous...)
	m := manifest{Contract: RetirementVersion, Review: Review{Handle: id, Scope: r.Scope, ExpiresAt: now.Add(5 * time.Minute)}, CreatedAt: now, Retirement: &copy}
	m.MAC, err = s.mac(dir, m)
	if err != nil {
		return Review{}, err
	}
	raw, err := json.Marshal(m)
	if err != nil || writeNew(dir, "manifest.json", raw, 0600, 0, 0) != nil || syncRoot(dir) != nil || syncRoot(s.root) != nil {
		return Review{}, ErrUnavailable
	}
	return m.Review, nil
}
