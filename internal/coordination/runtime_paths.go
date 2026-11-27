//go:build linux || darwin

package coordination

import (
	"net"
	"path/filepath"
)

type runtimeView struct{ Mount, Root NamespaceID }

func sameRuntimeView(a, b runtimeView) bool {
	return a.Mount.Device != 0 && a.Mount.Inode != 0 && a.Root.Device != 0 && a.Root.Inode != 0 && a == b
}

type RuntimeFile struct {
	Path     string
	UID, GID int64
	Mode     uint32
}

func cloneNamespace(p NamespaceProfile) *NamespaceProfile {
	p.UIDMap = append([]IDMap(nil), p.UIDMap...)
	p.GIDMap = append([]IDMap(nil), p.GIDMap...)
	return &p
}
func validateRuntimeFiles(p NamespaceProfile, root string, files []RuntimeFile) error {
	if !p.Valid() || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || len(files) > 4096 {
		return ErrNamespace
	}
	for _, f := range files {
		if !filepath.IsAbs(f.Path) || filepath.Clean(f.Path) != f.Path || f.UID < 0 || f.GID < 0 || f.UID >= int64(maxID) || f.GID >= int64(maxID) || !p.Owners(uint32(f.UID), uint32(f.GID)) || (f.Mode != 0400 && f.Mode != 0440) {
			return ErrNamespace
		}
		rel, err := filepath.Rel(root, f.Path)
		if err != nil || rel == "." || rel == ".." || len(rel) > 3 && rel[:3] == "../" {
			return ErrNamespace
		}
	}
	return nil
}

// CheckRuntime requires immutable authenticated mount inputs from the store.
// Namespace profile/connection evidence is private to the qualifier. Callers
// cannot enable this by presenting a digest or a matching socket string.
func (t *Target) CheckRuntime(root string, files []RuntimeFile) error {
	if t == nil || t.namespaceProfile == nil || validateRuntimeFiles(*t.namespaceProfile, root, files) != nil {
		return ErrNamespace
	}
	conn, ok := t.conn.(*net.UnixConn)
	if !ok {
		return ErrNamespace
	}
	return checkSharedRuntime(conn, *t.namespaceProfile, root, files)
}
