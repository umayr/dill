//go:build linux

package secretfile

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

type peer struct {
	cred        unix.Ucred
	proc, pidfd int
	snapshot    processIdentity
}
type processIdentity struct {
	start      string
	executable fileID
	userNS     fileID
	mapping    Mapping
}

func statID(fd int) (fileID, error) {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return fileID{}, config.ErrSecretUnavailable
	}
	return fileID{uint64(st.Dev), st.Ino}, nil
}

func readProc(fd int, name string) ([]byte, error) {
	f, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	file := os.NewFile(uintptr(f), "process metadata")
	defer file.Close()
	b, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(b) > 65536 {
		return nil, config.ErrSecretUnavailable
	}
	return b, nil
}
func procMap(fd int, name string) ([]IDRange, error) {
	b, err := readProc(fd, name)
	if err != nil {
		return nil, err
	}
	return ParseIDMap(bytes.NewReader(b))
}
func namespace(fd int, name string) (int, error) {
	// proc namespace descriptors are kernel-provided magic links, not submitted
	// paths. Following these and exe/root below is intentional and constrained.
	return unix.Openat(fd, "ns/"+name, unix.O_RDONLY|unix.O_CLOEXEC, 0)
}
func identityMap(m []IDRange) bool {
	return len(m) == 1 && m[0] == (IDRange{0, 0, 4294967295})
}
func sameRanges(a, b []IDRange) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x == y {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func sameIdentity(a, b processIdentity) bool {
	return a.start == b.start && a.executable == b.executable && a.userNS == b.userNS && sameRanges(a.mapping.UID, b.mapping.UID) && sameRanges(a.mapping.GID, b.mapping.GID)
}

// getInt is the narrow syscall seam for exercising kernels without
// SO_PEERPIDFD; there is deliberately no pidfd_open(pid) fallback.
func socketPeer(fd int, getInt func(int, int, int) (int, error)) (*unix.Ucred, int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return nil, -1, config.ErrSecretUnavailable
	}
	pidfd, err := getInt(fd, unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	if err != nil {
		return nil, -1, config.ErrSecretUnavailable
	}
	return cred, pidfd, nil
}

func capturePeer(conn *net.UnixConn, engine string) (*peer, error) {
	p := &peer{proc: -1, pidfd: -1}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, config.ErrSecretUnavailable
	}
	var cred *unix.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, p.pidfd, sockErr = socketPeer(int(fd), unix.GetsockoptInt)
	})
	if err != nil || sockErr != nil || cred == nil || cred.Pid <= 1 || p.pidfd < 0 {
		p.close()
		return nil, config.ErrSecretUnavailable
	}
	unix.CloseOnExec(p.pidfd)
	p.cred = *cred
	// Rootful peers must really be root; a rootless peer must be this operator.
	if cred.Uid != 0 && (engine != "podman" || cred.Uid != uint32(os.Geteuid())) {
		p.close()
		return nil, config.ErrSecretUnavailable
	}
	p.proc, err = unix.Open(fmt.Sprintf("/proc/%d", cred.Pid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		p.close()
		return nil, config.ErrSecretUnavailable
	}
	p.snapshot, err = p.inspect(engine)
	if err != nil {
		p.close()
		return nil, config.ErrSecretUnavailable
	}
	return p, nil
}
func (p *peer) close() {
	if p.proc >= 0 {
		unix.Close(p.proc)
	}
	if p.pidfd >= 0 {
		unix.Close(p.pidfd)
	}
}
func (p *peer) alive() bool {
	fds := []unix.PollFd{{Fd: int32(p.pidfd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	return err == nil && n == 0
}

func (p *peer) inspect(engine string) (processIdentity, error) {
	fail := func() (processIdentity, error) { return processIdentity{}, config.ErrSecretUnavailable }
	if !p.alive() {
		return fail()
	}
	stat, err := readProc(p.proc, "stat")
	if err != nil {
		return fail()
	}
	start, ok := processStart(stat, p.cred.Pid)
	if !ok {
		return fail()
	}
	status, err := readProc(p.proc, "status")
	if err != nil || !processCredentials(status, p.cred) {
		return fail()
	}
	uid, err := procMap(p.proc, "uid_map")
	if err != nil {
		return fail()
	}
	gid, err := procMap(p.proc, "gid_map")
	if err != nil {
		return fail()
	}
	ns, err := namespace(p.proc, "user")
	if err != nil {
		return fail()
	}
	defer unix.Close(ns)
	nsID, err := statID(ns)
	if err != nil {
		return fail()
	}
	self, err := unix.Open("/proc/self/ns/user", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	defer unix.Close(self)
	selfID, err := statID(self)
	if err != nil {
		return fail()
	}
	var parentID fileID
	if p.cred.Uid != 0 {
		// Only a direct child of the operator's initial namespace is supported.
		parent, e := unix.IoctlRetInt(ns, unix.NS_GET_PARENT)
		if e != nil {
			return fail()
		}
		parentID, e = statID(parent)
		unix.Close(parent)
		if e != nil {
			return fail()
		}
	}
	if !namespaceMapping(p.cred, uint32(os.Geteuid()), selfID, nsID, parentID, Mapping{uid, gid}) {
		return fail()
	}
	// A different chroot/VM filesystem is not the local host target.
	root, err := unix.Openat(p.proc, "root", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail()
	}
	rootID, err := statID(root)
	unix.Close(root)
	local, e := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return fail()
	}
	localID, e := statID(local)
	unix.Close(local)
	if err != nil || e != nil || rootID != localID {
		return fail()
	}
	exe, err := p.executable(engine)
	if err != nil || !p.alive() {
		return fail()
	}
	return processIdentity{start, exe, nsID, Mapping{uid, gid}}, nil
}

func namespaceMapping(cred unix.Ucred, operator uint32, self, daemon, parent fileID, maps Mapping) bool {
	if cred.Uid == 0 {
		return self == daemon && identityMap(maps.UID) && identityMap(maps.GID)
	}
	if cred.Uid != operator || parent != self || daemon == self {
		return false
	}
	u, ok := translate(maps.UID, 0)
	g, gok := translate(maps.GID, 0)
	return ok && gok && u == int64(cred.Uid) && g == int64(cred.Gid)
}

func processStart(b []byte, pid int32) (string, bool) {
	s := string(b)
	prefix := strconv.Itoa(int(pid)) + " ("
	end := strings.LastIndex(s, ") ")
	if !strings.HasPrefix(s, prefix) || end < len(prefix) {
		return "", false
	}
	fields := strings.Fields(s[end+2:])
	if len(fields) < 20 {
		return "", false
	}
	n, err := strconv.ParseUint(fields[19], 10, 64) // starttime is field 22
	return fields[19], err == nil && n > 0
}
func processCredentials(b []byte, cred unix.Ucred) bool {
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "Uid:" && fields[0] != "Gid:") {
			continue
		}
		if len(fields) != 5 || seen[fields[0]] {
			return false
		}
		seen[fields[0]] = true
		want := cred.Uid
		if fields[0] == "Gid:" {
			want = cred.Gid
		}
		for _, f := range fields[1:] {
			n, err := strconv.ParseUint(f, 10, 32)
			if err != nil || n != uint64(want) {
				return false
			}
		}
	}
	return len(seen) == 2
}
func (p *peer) executable(engine string) (fileID, error) {
	var buf [4096]byte
	n, err := unix.Readlinkat(p.proc, "exe", buf[:])
	if err != nil || n == len(buf) {
		return fileID{}, config.ErrSecretUnavailable
	}
	name := string(buf[:n])
	want := "podman"
	if engine == "docker" {
		want = "dockerd"
	}
	if path.Base(name) != want {
		return fileID{}, config.ErrSecretUnavailable
	}
	parent, leaf, err := openParent(name, p.cred.Uid)
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
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || (st.Uid != 0 && st.Uid != p.cred.Uid) || st.Mode&0022 != 0 || !noACL(fd, false) {
		return fileID{}, config.ErrSecretUnavailable
	}
	actual, err := unix.Openat(p.proc, "exe", unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return fileID{}, config.ErrSecretUnavailable
	}
	defer unix.Close(actual)
	id, err := statID(actual)
	if err != nil || id != (fileID{uint64(st.Dev), st.Ino}) {
		return fileID{}, config.ErrSecretUnavailable
	}
	return id, nil
}
