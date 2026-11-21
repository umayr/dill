// Package projectsnapshot validates and privately stages in-memory project snapshots.
// It does not establish consent, evaluate configuration, or deploy anything.
package projectsnapshot

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// Entry is a regular file; SHA256 is the digest of Content. Callers must not
// mutate a snapshot concurrently with Receive.
type Entry struct {
	Path    string
	Content []byte
	SHA256  [32]byte
}
type Snapshot struct {
	Entrypoint string
	Files      []Entry
}

// ErrPublicationUnknown means publication or its durability cannot be determined.
// Any stage remnants must be retained for inspection, not automatically retried.
var ErrPublicationUnknown = errors.New("snapshot publication durability unknown")

var (
	// ErrStagingCapacity means the proposed stage cannot fit in the root's budget.
	ErrStagingCapacity = errors.New("snapshot staging capacity exhausted")
	// ErrStagingBusy means another receiver holds the root's admission lock.
	ErrStagingBusy = errors.New("snapshot staging admission busy")
	// ErrStagingInventory means the receiver could not safely account for the root.
	ErrStagingInventory = errors.New("snapshot staging inventory unavailable")
)

// CleanupError distinguishes a failed receive whose identity-bound cleanup
// also failed. Its wrapped errors can contain paths and must not be logged.
type CleanupError struct{ Cause, Cleanup error }

func (e *CleanupError) Error() string   { return "snapshot receive failed and cleanup failed" }
func (e *CleanupError) Unwrap() []error { return []error{e.Cause, e.Cleanup} }

// Limits bounds one receive; Directories includes the stage root.
type Limits struct {
	Files, Directories, Depth, PathBytes, ComponentBytes int
	FileBytes, TotalBytes                                int64
}

// MaximumLimits returns the immutable implementation ceilings.
func MaximumLimits() Limits {
	return Limits{Files: 4096, Directories: 256, Depth: 32, PathBytes: 1024, ComponentBytes: 255, FileBytes: 16 << 20, TotalBytes: 64 << 20}
}

// Validate applies the supplied positive limits, which may only lower hard limits.
func Validate(ctx context.Context, s Snapshot, l Limits) error {
	if ctx == nil {
		return fmt.Errorf("nil context")
	}
	h := MaximumLimits()
	if l.Files <= 0 || l.Files > h.Files || l.Directories <= 0 || l.Directories > h.Directories || l.Depth <= 0 || l.Depth > h.Depth || l.PathBytes <= 0 || l.PathBytes > h.PathBytes || l.ComponentBytes <= 0 || l.ComponentBytes > h.ComponentBytes || l.FileBytes <= 0 || l.FileBytes > h.FileBytes || l.TotalBytes <= 0 || l.TotalBytes > h.TotalBytes {
		return fmt.Errorf("invalid limits")
	}
	if len(s.Files) > l.Files {
		return fmt.Errorf("too many files")
	}
	files := map[string]bool{}
	dirs := map[string]string{}
	var total int64
	found := false
	for _, e := range s.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validPath(e.Path, l); err != nil {
			return err
		}
		n := int64(len(e.Content))
		if n > l.FileBytes || n > l.TotalBytes-total {
			return fmt.Errorf("content limit exceeded")
		}
		total += n
		key := strings.ToLower(e.Path)
		if files[key] || dirs[key] != "" {
			return fmt.Errorf("duplicate or conflicting path: %q", e.Path)
		}
		parts := strings.Split(e.Path, "/")
		for i := 1; i < len(parts); i++ {
			p := strings.Join(parts[:i], "/")
			k := strings.ToLower(p)
			if files[k] {
				return fmt.Errorf("file ancestor: %q", p)
			}
			if prev := dirs[k]; prev != "" && prev != p {
				return fmt.Errorf("directory spelling collision: %q", p)
			}
			if dirs[k] == "" && len(dirs)+1 >= l.Directories {
				return fmt.Errorf("directory limit exceeded")
			}
			dirs[k] = p
		}
		files[key] = true
		if sha256.Sum256(e.Content) != e.SHA256 {
			return fmt.Errorf("content digest mismatch: %q", e.Path)
		}
		if e.Path == s.Entrypoint {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("entrypoint not present")
	}
	return ctx.Err()
}
func validPath(p string, l Limits) error {
	if len(p) == 0 || len(p) > l.PathBytes {
		return fmt.Errorf("invalid path length")
	}
	parts := strings.Split(p, "/")
	if len(parts) > l.Depth {
		return fmt.Errorf("path too deep")
	}
	for _, c := range parts {
		if len(c) == 0 || len(c) > l.ComponentBytes || c == "." || c == ".." || strings.EqualFold(c, ".git") || c[len(c)-1] == ' ' || c[len(c)-1] == '.' {
			return fmt.Errorf("invalid path component: %q", c)
		}
		for _, r := range c {
			if r < 32 || r > 126 || strings.ContainsRune(`\:<>"|?*`, r) {
				return fmt.Errorf("nonportable path: %q", p)
			}
		}
		base := strings.ToUpper(strings.SplitN(c, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return fmt.Errorf("reserved path: %q", p)
		}
	}
	return nil
}
