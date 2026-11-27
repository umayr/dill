package coordination

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

var ErrNamespace = errors.New("executor namespace qualification unavailable or mismatched")

const MaxIDMapEntries = 340
const MaxIDMapBytes = 32 << 10
const maxID = uint64(1<<32 - 1) // exclusive: Linux reserves UID/GID -1

type IDMap struct{ Inside, Outside, Length uint32 }

// NamespaceProfile is independently provisioned operator metadata, not authority
// supplied by a deployment request. Numeric owners are container-relative only
// after the executor and engine namespace have been independently matched.
type NamespaceProfile struct {
	Rootless       bool
	UIDMap, GIDMap []IDMap
}

func validMap(m []IDMap) bool {
	if len(m) == 0 || len(m) > MaxIDMapEntries {
		return false
	}
	for n, a := range m {
		if a.Length == 0 || uint64(a.Inside)+uint64(a.Length) > maxID || uint64(a.Outside)+uint64(a.Length) > maxID {
			return false
		}
		for _, b := range m[:n] {
			if uint64(a.Inside) < uint64(b.Inside)+uint64(b.Length) && uint64(b.Inside) < uint64(a.Inside)+uint64(a.Length) {
				return false
			}
			if uint64(a.Outside) < uint64(b.Outside)+uint64(b.Length) && uint64(b.Outside) < uint64(a.Outside)+uint64(a.Length) {
				return false
			}
		}
	}
	return true
}
func mapID(m []IDMap, id uint32) (uint32, bool) {
	for _, r := range m {
		if id >= r.Inside && uint64(id) < uint64(r.Inside)+uint64(r.Length) {
			return r.Outside + (id - r.Inside), true
		}
	}
	return 0, false
}
func (p NamespaceProfile) Valid() bool {
	if !validMap(p.UIDMap) || !validMap(p.GIDMap) {
		return false
	}
	uid, u := mapID(p.UIDMap, 0)
	_, g := mapID(p.GIDMap, 0)
	if !u || !g {
		return false
	}
	if p.Rootless {
		if uid == 0 {
			return false
		}
		for _, m := range [][]IDMap{p.UIDMap, p.GIDMap} {
			for _, r := range m {
				if r.Outside == 0 {
					return false
				}
			}
		}
		return true
	}
	return len(p.UIDMap) == 1 && len(p.GIDMap) == 1 && p.UIDMap[0] == (IDMap{0, 0, uint32(maxID)}) && p.GIDMap[0] == (IDMap{0, 0, uint32(maxID)})
}

// Owners verifies that both reviewed container-relative IDs are mapped. It does
// not chown host IDs: the qualified executor must run inside this namespace.
func (p NamespaceProfile) Owners(uid, gid uint32) bool {
	if !p.Valid() {
		return false
	}
	_, u := mapID(p.UIDMap, uid)
	_, g := mapID(p.GIDMap, gid)
	return u && g
}
func (p NamespaceProfile) Digest() (string, error) {
	if !p.Valid() {
		return "", ErrNamespace
	}
	p.UIDMap = append([]IDMap(nil), p.UIDMap...)
	p.GIDMap = append([]IDMap(nil), p.GIDMap...)
	for _, m := range [][]IDMap{p.UIDMap, p.GIDMap} {
		sort.Slice(m, func(i, j int) bool { return m[i].Inside < m[j].Inside })
	}
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(append([]byte("dill.namespace-profile/1\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
func ParseIDMap(data []byte) ([]IDMap, error) {
	if len(data) == 0 || len(data) > MaxIDMapBytes {
		return nil, ErrNamespace
	}
	for _, c := range data {
		if (c < '0' || c > '9') && c != ' ' && c != '\t' && c != '\n' {
			return nil, ErrNamespace
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > MaxIDMapEntries {
		return nil, ErrNamespace
	}
	out := make([]IDMap, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, ErrNamespace
		}
		var numbers [3]uint32
		for n, s := range fields {
			if s == "" {
				return nil, ErrNamespace
			}
			for _, c := range s {
				if c < '0' || c > '9' {
					return nil, ErrNamespace
				}
			}
			v, e := strconv.ParseUint(s, 10, 32)
			if e != nil {
				return nil, ErrNamespace
			}
			numbers[n] = uint32(v)
		}
		out = append(out, IDMap{numbers[0], numbers[1], numbers[2]})
	}
	if !validMap(out) {
		return nil, ErrNamespace
	}
	return out, nil
}

type NamespaceID struct{ Device, Inode uint64 }
type NamespaceEvidence struct {
	ID      NamespaceID
	Profile NamespaceProfile
}

// MatchNamespaceEvidence denies different namespaces even when maps coincide.
// A digest is a commitment to independently observed evidence, not a grant.
func MatchNamespaceEvidence(expected NamespaceProfile, executor, engine NamespaceEvidence) error {
	digest, err := expected.Digest()
	if err != nil || executor.ID.Device == 0 || executor.ID.Inode == 0 || executor.ID != engine.ID {
		return ErrNamespace
	}
	a, err := executor.Profile.Digest()
	if err != nil || a != digest {
		return ErrNamespace
	}
	b, err := engine.Profile.Digest()
	if err != nil || b != digest {
		return ErrNamespace
	}
	return nil
}
