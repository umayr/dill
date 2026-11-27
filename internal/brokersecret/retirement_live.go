package brokersecret

// permitsLive narrows retirement adoption to its explicit predecessor set.
// This syntax/set check never replaces the subsequent active manifest MAC check.
func (m *Materialized) permitsLive(handle, generation string) bool {
	if m == nil || !validID(handle) || !validID(generation) {
		return false
	}
	if !m.IsRetirement() {
		return true
	}
	if !m.retirement.Valid() || m.retirement.Scope != m.review.Scope {
		return false
	}
	if handle == m.review.Handle && generation == m.review.Scope.Generation {
		return true
	}
	for _, p := range m.retirement.Previous {
		if p.Handle == handle && p.Scope.Generation == generation {
			return true
		}
	}
	return false
}
