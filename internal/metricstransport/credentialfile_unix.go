//go:build darwin || linux

package metricstransport

import (
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

var errCredentialFile = errors.New("credential must be a bounded private regular file in a trusted directory")

func readCredential(path string, limit int64, reserved []os.FileInfo) ([]byte, error) {
	components := strings.Split(path[1:], "/") // validated clean and absolute
	uid := uint32(os.Geteuid())
	dir, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errCredentialFile
	}
	defer func() { _ = unix.Close(dir) }()
	if !trustedDirectory(dir, uid) {
		return nil, errCredentialFile
	}
	for _, name := range components[:len(components)-1] {
		next, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, errCredentialFile
		}
		_ = unix.Close(dir)
		dir = next
		if !trustedDirectory(dir, uid) {
			return nil, errCredentialFile
		}
	}

	name := components[len(components)-1]
	var before unix.Stat_t
	if unix.Fstatat(dir, name, &before, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateFile(before, uid, limit) {
		return nil, errCredentialFile
	}
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errCredentialFile
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var after unix.Stat_t
	if unix.Fstat(fd, &after) != nil || !privateFile(after, uid, limit) ||
		after.Dev != before.Dev || after.Ino != before.Ino || !noExtendedACL(fd) {
		return nil, errCredentialFile
	}
	if len(reserved) != 0 {
		info, err := f.Stat()
		if err != nil {
			return nil, errCredentialFile
		}
		for _, other := range reserved {
			if os.SameFile(info, other) {
				return nil, errCredentialFile
			}
		}
	}
	contents, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(contents)) > limit {
		clear(contents)
		return nil, errCredentialFile
	}
	return contents, nil
}

func trustedDirectory(fd int, uid uint32) bool {
	var stat unix.Stat_t
	// A root-owned sticky directory (for example /tmp) cannot let a
	// different unprivileged user replace root-/agent-owned entries.
	return unix.Fstat(fd, &stat) == nil && (stat.Uid == 0 || stat.Uid == uid) &&
		(stat.Mode&0022 == 0 || (stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0)) && noExtendedACL(fd)
}

func privateFile(stat unix.Stat_t, uid uint32, limit int64) bool {
	return stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&0077 == 0 && stat.Uid == uid &&
		stat.Nlink == 1 && stat.Size > 0 && stat.Size <= limit
}
