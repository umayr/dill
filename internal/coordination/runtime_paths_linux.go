//go:build linux

package coordination

import (
	"golang.org/x/sys/unix"
	"net"
	"os"
	"strconv"
	"strings"
)

func peerPID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, ErrNamespace
	}
	var cred *unix.Ucred
	var e error
	if raw.Control(func(fd uintptr) { cred, e = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }) != nil || e != nil || cred == nil || cred.Pid <= 0 || cred.Uid != 0 || cred.Gid != 0 {
		return 0, ErrNamespace
	}
	return int(cred.Pid), nil
}
func procView(pid int) (*os.Root, *os.File, error) {
	root, e := os.OpenRoot("/proc/" + strconv.Itoa(pid))
	if e != nil {
		return nil, nil, ErrNamespace
	}
	dir, e := root.Open(".")
	if e != nil {
		root.Close()
		return nil, nil, ErrNamespace
	}
	return root, dir, nil
}
func runtimeViewOf(f *os.File) (runtimeView, error) {
	var m, r unix.Stat_t
	if unix.Fstatat(int(f.Fd()), "ns/mnt", &m, 0) != nil || unix.Fstatat(int(f.Fd()), "root", &r, 0) != nil {
		return runtimeView{}, ErrNamespace
	}
	return runtimeView{Mount: NamespaceID{uint64(m.Dev), m.Ino}, Root: NamespaceID{uint64(r.Dev), r.Ino}}, nil
}
func runtimeStat(path string) (unix.Stat_t, error) {
	var out unix.Stat_t
	fd, e := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return out, ErrNamespace
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for n, p := range parts {
		flags := unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if n < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if e != nil {
			return out, ErrNamespace
		}
		fd = next
	}
	defer unix.Close(fd)
	if unix.Fstat(fd, &out) != nil || out.Mode&unix.S_IFMT == unix.S_IFLNK {
		return out, ErrNamespace
	}
	return out, nil
}
func checkSharedRuntime(conn *net.UnixConn, p NamespaceProfile, root string, files []RuntimeFile) error {
	if CheckNamespacePeer(conn, p) != nil {
		return ErrNamespace
	}
	pid, e := peerPID(conn)
	if e != nil {
		return ErrNamespace
	}
	self, a, e := procView(os.Getpid())
	if e != nil {
		return ErrNamespace
	}
	defer self.Close()
	defer a.Close()
	peer, b, e := procView(pid)
	if e != nil {
		return ErrNamespace
	}
	defer peer.Close()
	defer b.Close()
	// Equal user maps alone do not prove bind paths mean the same thing.
	initial, err := runtimeViewOf(a)
	if err != nil {
		return ErrNamespace
	}
	observed, err := runtimeViewOf(b)
	if err != nil || !sameRuntimeView(initial, observed) {
		return ErrNamespace
	}
	stat, e := runtimeStat(root)
	if e != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&07777 != 0700 || stat.Uid != 0 {
		return ErrNamespace
	}
	for _, f := range files {
		s, e := runtimeStat(f.Path)
		if e != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Nlink != 1 || s.Uid != uint32(f.UID) || s.Gid != uint32(f.GID) || s.Mode&07777 != f.Mode {
			return ErrNamespace
		}
	}
	// Repeat shared-view checks after path observation to reject observed changes.
	current, err := runtimeViewOf(a)
	if err != nil {
		return ErrNamespace
	}
	observed, err = runtimeViewOf(b)
	if err != nil || !sameRuntimeView(initial, current) || !sameRuntimeView(initial, observed) || CheckNamespacePeer(conn, p) != nil {
		return ErrNamespace
	}
	return nil
}
