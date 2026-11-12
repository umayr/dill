//go:build linux

package secretfile

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func publishPrivate(path string, data []byte) (func(bool), error) {
	parent, leaf, err := openParent(path, uint32(os.Geteuid()))
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	keepParent := false
	tmp := ""
	defer func() {
		if tmp != "" {
			unix.Unlinkat(parent, tmp, 0)
		}
		if !keepParent {
			unix.Close(parent)
		}
	}()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, config.ErrSecretUnavailable
	}
	tmp = ".dill-proof-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(parent, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	f := os.NewFile(uintptr(fd), "private preview")
	defer f.Close()

	var own unix.Stat_t
	if unix.Fchmod(fd, 0600) != nil || unix.Fstat(fd, &own) != nil || !noACL(fd, false) {
		return nil, config.ErrSecretUnavailable
	}
	if _, err = f.Write(data); err != nil || f.Sync() != nil {
		return nil, config.ErrSecretUnavailable
	}
	if f.Close() != nil {
		return nil, config.ErrSecretUnavailable
	}
	// RENAME_NOREPLACE is required. No check-then-overwrite or link-count window.
	if unix.Renameat2(parent, tmp, parent, leaf, unix.RENAME_NOREPLACE) != nil {
		return nil, config.ErrSecretUnavailable
	}
	tmp = ""
	finish := func(keep bool) {
		if !keep {
			var current unix.Stat_t
			if unix.Fstatat(parent, leaf, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == own.Dev && current.Ino == own.Ino {
				unix.Unlinkat(parent, leaf, 0)
			}
		}
		unix.Close(parent)
	}
	// Persist the directory entry as well as the complete file before success.
	dir, err := unix.Openat(parent, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		keepParent = true
		finish(false)
		return nil, config.ErrSecretUnavailable
	}
	err = unix.Fsync(dir)
	unix.Close(dir)
	if err != nil {
		keepParent = true
		finish(false)
		return nil, config.ErrSecretUnavailable
	}
	keepParent = true
	return finish, nil
}

// Metadata only, also detecting bind-mount aliases before a private key read.
func metadataIdentity(path string) (fileID, error) {
	parent, leaf, err := openParent(path, uint32(os.Geteuid()))
	if err != nil {
		return fileID{}, config.ErrSecretUnavailable
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, leaf, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileID{}, config.ErrSecretUnavailable
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fileID{}, config.ErrSecretUnavailable
	}
	return fileID{uint64(st.Dev), st.Ino}, nil
}

// Compare directory inode + final name even when the final source is absent.
// A bind-mounted directory alias must not turn proof output into provisioning.
func samePrivateEntry(a, b string) bool {
	pa, la, err := openParent(a, uint32(os.Geteuid()))
	if err != nil {
		return false
	}
	defer unix.Close(pa)
	// Metadata-only alias discovery deliberately does not authorize the source
	// ancestry: even an unselected, unsafe catalog path must not alias output.
	pb, err := unix.Open(filepath.Dir(b), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(pb)
	lb := filepath.Base(b)
	ia, ea := statID(pa)
	ib, eb := statID(pb)
	return ea == nil && eb == nil && ia == ib && la == lb
}

// Probe every catalog source without byte access, including unsafe/unselected
// paths, solely to exclude source aliases BEFORE reading the private key.
func sourceMetadataIdentity(p string) (fileID, error) {
	fd, err := unix.Open(p, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileID{}, err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return fileID{}, config.ErrSecretUnavailable
	}
	id, err := statID(fd)
	return id, err
}
