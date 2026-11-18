package operation

import (
	"sort"

	"github.com/umayr/dill/internal/preflight"
)

// ManagedResource is private evidence exchanged between the combined agent and
// its checked CLI child. Docker volumes have no immutable ID and must never be
// represented as immutable evidence.
type ManagedResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

// ManagedExpectation constrains which identities may be present inside the
// same coordination session that compares the named preflight digest.
type ManagedExpectation struct {
	Mode      string            `json:"mode"`
	Resources []ManagedResource `json:"resources"`
}

func (m ManagedExpectation) Valid() bool {
	if m.Resources == nil || len(m.Resources) > preflight.MaxResources {
		return false
	}
	if m.Mode == "create" {
		return len(m.Resources) == 0
	}
	if m.Mode != "update" && m.Mode != "remove" {
		return false
	}
	seen := make(map[string]bool, len(m.Resources))
	networks, containers := 0, 0
	for _, r := range m.Resources {
		if !preflight.ValidName(r.Name) || !dockerID(r.ID) || seen[r.Kind+"\x00"+r.Name] {
			return false
		}
		seen[r.Kind+"\x00"+r.Name] = true
		switch r.Kind {
		case "container":
			containers++
		case "network":
			networks++
		default:
			return false
		}
	}
	return networks == 1 && containers > 0
}

func dockerID(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Matches checks the whole named inventory, not just the selected IDs.
func (m ManagedExpectation) Matches(resources []preflight.Resource) bool {
	if !m.Valid() || len(resources) == 0 {
		return false
	}
	remaining := make(map[string]string, len(m.Resources))
	for _, r := range m.Resources {
		remaining[r.Kind+"\x00"+r.Name] = r.ID
	}
	for _, r := range resources {
		if r.Presence != "present" {
			continue
		}
		if r.Kind == "volume" {
			// Docker provides only a reusable name, never an immutable volume
			// ID. Retention is safe; claiming it for an ensure is not.
			if m.Mode == "create" || r.Action != "retain" {
				return false
			}
			continue
		}
		key := r.Kind + "\x00" + r.Name
		if r.IdentityKind != "immutable-id" || remaining[key] != r.Identity || remaining[key] == "" {
			return false
		}
		delete(remaining, key)
	}
	return len(remaining) == 0
}

func SortManaged(rows []ManagedResource) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Name < rows[j].Name
	})
}
