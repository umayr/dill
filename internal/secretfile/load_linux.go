//go:build linux

package secretfile

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

// openParent pins each directory while traversing; no submitted component may
// be a symlink. This is not protection against the trusted administrator moving
// directories after the check: lifetime immutability remains mandatory.
func openParent(p string, owner uint32) (int, string, error) {
	if !cleanPath(p) {
		return -1, "", config.ErrSecretUnavailable
	}
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", config.ErrSecretUnavailable
	}
	parts := strings.Split(p[1:], "/")
	for _, component := range parts[:len(parts)-1] {
		if !safeDir(fd, owner) {
			unix.Close(fd)
			return -1, "", config.ErrSecretUnavailable
		}
		next, e := unix.Openat(fd, component, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return -1, "", config.ErrSecretUnavailable
		}
		fd = next
	}
	if !safeDir(fd, owner) {
		unix.Close(fd)
		return -1, "", config.ErrSecretUnavailable
	}
	return fd, parts[len(parts)-1], nil
}

// readPrivate reads only trusted metadata documents, never source files. Size
// and metadata are checked before and after a bounded read (atime is excluded).
func readPrivate(p string, owner uint32) ([]byte, error) {
	parent, name, err := openParent(p, owner)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	defer unix.Close(parent)
	pinned, err := unix.Openat(parent, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	defer unix.Close(pinned)
	var before, opened, after unix.Stat_t
	if unix.Fstat(pinned, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Uid != owner || (before.Mode&07777 != 0400 && before.Mode&07777 != 0600) || before.Size < 0 || before.Size > 65536 || !noACL(pinned, false) {
		return nil, config.ErrSecretUnavailable
	}
	// Reopen only the validated regular inode, never an unchecked device/FIFO or
	// a separately resolved filename. The proc link is kernel-controlled.
	fd, err := unix.Open("/proc/self/fd/"+strconv.Itoa(pinned), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	f := os.NewFile(uintptr(fd), "secret metadata")
	defer f.Close()
	if unix.Fstat(fd, &opened) != nil || !sameMetadata(before, opened) {
		return nil, config.ErrSecretUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || int64(len(b)) != before.Size || unix.Fstat(fd, &after) != nil || !sameMetadata(before, after) || !noACL(fd, false) {
		clear(b)
		return nil, config.ErrSecretUnavailable
	}
	return b, nil
}

func sameMetadata(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Uid == b.Uid && a.Gid == b.Gid && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
