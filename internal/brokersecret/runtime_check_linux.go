//go:build linux

package brokersecret

// CheckRuntime authenticates current generation evidence before handing private
// paths to a qualified executor verifier. The callback cannot reenter Store.
func (m *Materialized) CheckRuntime(check func(string, []Mount) error) error {
	if m == nil || m.store == nil || check == nil {
		return ErrUnavailable
	}
	s := m.store
	s.mu.Lock()
	defer s.mu.Unlock()
	manifest, dir, err := s.load(m.review.Handle, m.review.Scope)
	if err != nil {
		return ErrUnavailable
	}
	defer dir.Close()
	fresh := s.materialized(manifest)
	return check(s.path, fresh.Mounts())
}
