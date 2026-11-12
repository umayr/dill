//go:build linux

package secretfile

import (
	"strconv"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func supportedPlatform() bool { return true }

// O_PATH descriptors are not byte-readable and cannot use fgetxattr. Query
// ACLs through the kernel descriptor link to the pinned inode, never through
// the submitted path. POSIX ACL reads need no VFS source read permission;
// filesystem/LSM/procfs failures still reject the operation.
func noACL(fd int, dir bool) bool {
	attrs := []string{"system.posix_acl_access"}
	if dir {
		attrs = append(attrs, "system.posix_acl_default")
	}
	for _, name := range attrs {
		_, err := unix.Getxattr("/proc/self/fd/"+strconv.Itoa(fd), name, nil)
		if err != unix.ENODATA {
			return false
		}
	}
	return true
}

func safeDir(fd int, operator uint32) bool {
	var st unix.Stat_t
	return unix.Fstat(fd, &st) == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR && (st.Uid == 0 || st.Uid == operator) && st.Mode&0022 == 0 && noACL(fd, true)
}
func checkSource(s config.ResolvedSecret) (fileID, error) {
	fail := func() (fileID, error) { return fileID{}, config.ErrSecretUnavailable }
	if s.HostMode != 256 && s.HostMode != 288 {
		return fail()
	}
	fd, name, err := openParent(s.HostPath, uint32(unix.Geteuid()))
	if err != nil {
		return fail()
	}
	defer unix.Close(fd)
	file, err := unix.Openat(fd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	defer unix.Close(file)
	var st unix.Stat_t
	if unix.Fstat(file, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || int64(st.Uid) != s.HostUID || int64(st.Gid) != s.HostGID || int64(st.Mode&07777) != s.HostMode || !noACL(file, false) {
		return fail()
	}
	return fileID{device: uint64(st.Dev), inode: st.Ino}, nil
}
