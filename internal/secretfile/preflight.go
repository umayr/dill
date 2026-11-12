// Package secretfile checks external secret file metadata without reading file contents.
// Successful checks do not remove the requirement for trusted immutable source paths.
package secretfile

import "github.com/umayr/dill/internal/config"

// Mapping translates container IDs to host IDs. A nil mapping is not sufficient
// evidence of the engine's user namespace and is rejected.
type Mapping struct{ UID, GID []IDRange }

type IDRange struct{ Container, Host, Size uint64 }

func validRanges(ranges []IDRange) bool {
	if len(ranges) == 0 || len(ranges) > 340 {
		return false
	}
	for i, r := range ranges {
		// Linux reserves (uid_t)-1; no valid extent may include it.
		if r.Size == 0 || r.Container >= 4294967295 || r.Host >= 4294967295 || r.Size > 4294967295-r.Container || r.Size > 4294967295-r.Host {
			return false
		}
		for _, old := range ranges[:i] {
			if overlaps(r.Container, r.Size, old.Container, old.Size) || overlaps(r.Host, r.Size, old.Host, old.Size) {
				return false
			}
		}
	}
	return true
}

func translate(ranges []IDRange, id int64) (int64, bool) {
	if id < 0 || id > 2147483647 || !validRanges(ranges) {
		return 0, false
	}
	var result uint64
	matches := 0
	for _, r := range ranges {
		if uint64(id) >= r.Container && uint64(id)-r.Container < r.Size {
			result = r.Host + (uint64(id) - r.Container)
			matches++
		}
	}
	if matches != 1 || result > 2147483647 {
		return 0, false
	}
	return int64(result), true
}

// Check checks all selected mounts, including aliases across services. It never
// reads source bytes or changes source metadata. The caller must establish the
// mapping's provenance and the quiescence guarantees in the contract.
func Check(services map[string][]config.ResolvedSecret, mapping Mapping) error {
	if !supportedPlatform() || !validRanges(mapping.UID) || !validRanges(mapping.GID) {
		return config.ErrSecretUnavailable
	}
	aliases := map[fileID]permissions{}
	for _, mounts := range services {
		for _, s := range mounts {
			uid, ok := translate(mapping.UID, s.UID)
			if !ok || uid != s.HostUID {
				return config.ErrSecretUnavailable
			}
			gid, ok := translate(mapping.GID, s.GID)
			if !ok || gid != s.HostGID || s.Mode != s.HostMode {
				return config.ErrSecretUnavailable
			}
			id, err := checkSource(s)
			if err != nil {
				return config.ErrSecretUnavailable
			}
			p := permissions{uid: uid, gid: gid, mode: s.Mode}
			if old, ok := aliases[id]; ok && old != p {
				return config.ErrSecretUnavailable
			}
			aliases[id] = p
		}
	}
	return nil
}

type fileID struct{ device, inode uint64 }
type permissions struct{ uid, gid, mode int64 }
