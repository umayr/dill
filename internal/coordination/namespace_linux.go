//go:build linux

package coordination

import (
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"strconv"
)

func processNamespace(pid int, rootless bool) (NamespaceEvidence, error) {
	var out NamespaceEvidence
	if pid <= 0 {
		return out, ErrNamespace
	}
	root, err := os.OpenRoot("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return out, ErrNamespace
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return out, ErrNamespace
	}
	defer dir.Close()
	var stat unix.Stat_t
	if unix.Fstatat(int(dir.Fd()), "ns/user", &stat, 0) != nil {
		return out, ErrNamespace
	}
	out.ID = NamespaceID{Device: uint64(stat.Dev), Inode: stat.Ino}
	out.Profile.Rootless = rootless
	for _, item := range []struct {
		name   string
		target *[]IDMap
	}{{"uid_map", &out.Profile.UIDMap}, {"gid_map", &out.Profile.GIDMap}} {
		f, e := root.OpenFile(item.name, os.O_RDONLY|unix.O_NOFOLLOW, 0)
		if e != nil {
			return NamespaceEvidence{}, ErrNamespace
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxIDMapBytes+1))
		f.Close()
		if e != nil {
			return NamespaceEvidence{}, ErrNamespace
		}
		*item.target, e = ParseIDMap(data)
		if e != nil {
			return NamespaceEvidence{}, ErrNamespace
		}
	}
	if !out.Profile.Valid() {
		return NamespaceEvidence{}, ErrNamespace
	}
	return out, nil
}

// CheckNamespacePeer observes only kernel process/socket evidence. Call with
// the retained engine connection; no PID, mapping or namespace path comes from
// HTTP/source. The operator must start the executor in the required namespace.
// This does not enter namespaces, provision state or grant engine authority.
func CheckNamespacePeer(conn *net.UnixConn, expected NamespaceProfile) error {
	if conn == nil || !expected.Valid() || os.Geteuid() != 0 || os.Getegid() != 0 {
		return ErrNamespace
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return ErrNamespace
	}
	var cred *unix.Ucred
	var credErr error
	if raw.Control(func(fd uintptr) { cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }) != nil || credErr != nil || cred == nil || cred.Pid <= 0 || cred.Uid != 0 || cred.Gid != 0 {
		return ErrNamespace
	}
	self, err := processNamespace(os.Getpid(), expected.Rootless)
	if err != nil {
		return ErrNamespace
	}
	peer, err := processNamespace(int(cred.Pid), expected.Rootless)
	if err != nil {
		return ErrNamespace
	}
	return MatchNamespaceEvidence(expected, self, peer)
}
