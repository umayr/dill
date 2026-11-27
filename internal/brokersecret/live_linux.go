//go:build linux

package brokersecret

import (
	"bytes"
	"encoding/json"
)

// activeReview uses candidate metadata only to locate its authenticated scope.
// No candidate field is accepted until the complete on-disk MAC is verified.
func (s *Store) activeReview(id string, current Scope) (Review, error) {
	if s == nil {
		return Review{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready() || !validID(id) {
		return Review{}, ErrUnavailable
	}
	i, e := s.root.Lstat(id)
	if e != nil || !i.IsDir() || i.Mode().Perm() != 0700 {
		return Review{}, ErrUnavailable
	}
	dir, e := s.root.OpenRoot(id)
	if e != nil {
		return Review{}, ErrUnavailable
	}
	raw, e := readPrivate(dir, "manifest.json", 256<<10, 0600, 0, 0)
	dir.Close()
	if e != nil || !uniqueJSON(raw) {
		return Review{}, ErrUnavailable
	}
	var candidate manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&candidate) != nil {
		return Review{}, ErrUnavailable
	}
	old := candidate.Review.Scope
	if old.Registration != current.Registration || old.Stack != current.Stack || old.Engine != current.Engine || old.TargetDigest != current.TargetDigest || old.EndpointDigest != current.EndpointDigest {
		return Review{}, ErrUnavailable
	}
	verified, opened, e := s.load(id, old)
	if e != nil {
		return Review{}, ErrUnavailable
	}
	opened.Close()
	if !verified.Review.Active {
		return Review{}, ErrUnavailable
	}
	return verified.Review, nil
}
