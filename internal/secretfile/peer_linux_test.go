//go:build linux

package secretfile

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func TestSocketPeerUnsupportedKernel(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	for _, failure := range []error{unix.ENOPROTOOPT, unix.EINVAL, unix.EPERM, unix.ESRCH} {
		called := false
		cred, fd, err := socketPeer(fds[0], func(sock, level, option int) (int, error) {
			called = true
			if sock != fds[0] || level != unix.SOL_SOCKET || option != unix.SO_PEERPIDFD {
				t.Fatal("wrong peer syscall")
			}
			return -1, failure
		})
		if !called || cred != nil || fd != -1 || err != config.ErrSecretUnavailable {
			t.Fatalf("unsupported peer pidfd must fail closed: %v", err)
		}
	}
}
func TestSocketPeerKernelIdentity(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	// A success is exercised where Linux >=6.5 is available; older kernels must
	// reject, not substitute pidfd_open on a potentially recycled numeric PID.
	cred, fd, err := socketPeer(fds[0], unix.GetsockoptInt)
	if err != nil {
		if err != config.ErrSecretUnavailable {
			t.Fatal(err)
		}
		if os.Getenv("DILL_TEST_ALPHA_SECRETS") == "1" {
			t.Fatal("required Linux SO_PEERPIDFD qualification failed")
		}
		t.Skip("SO_PEERPIDFD is an Alpha runtime prerequisite")
	}
	defer unix.Close(fd)
	if cred.Pid != int32(os.Getpid()) || cred.Uid != uint32(os.Geteuid()) || cred.Gid != uint32(os.Getegid()) {
		t.Fatal("peer identity mismatch")
	}
	p := peer{pidfd: fd}
	if !p.alive() {
		t.Fatal("live exact peer pidfd reported dead")
	}
}
func TestProcessIdentityParsing(t *testing.T) {
	stat := "123 (synthetic ) daemon) S " + strings.Repeat("0 ", 18) + "456 0 0\n"
	if start, ok := processStart([]byte(stat), 123); !ok || start != "456" {
		t.Fatal("proc stat with parentheses rejected")
	}
	for _, bad := range []string{"", "123 (x) S", strings.Replace(stat, "456", "bad", 1), strings.Replace(stat, "123", "124", 1)} {
		if _, ok := processStart([]byte(bad), 123); ok {
			t.Fatal("invalid process start identity accepted")
		}
	}
	cred := unix.Ucred{Uid: 1000, Gid: 1001}
	valid := "Name:\tsynthetic\nUid:\t1000 1000 1000 1000\nGid:\t1001 1001 1001 1001\n"
	if !processCredentials([]byte(valid), cred) {
		t.Fatal("matching credentials rejected")
	}
	for _, bad := range []string{strings.Replace(valid, "1000 1000 1000 1000", "1000 0 1000 1000", 1), strings.Replace(valid, "Gid:", "Other:", 1), valid + "Uid: 1000 1000 1000 1000\n", strings.Replace(valid, "1001 1001 1001 1001", "1001 1001 1001", 1)} {
		if processCredentials([]byte(bad), cred) {
			t.Fatal("ambiguous credentials accepted")
		}
	}
	base := processIdentity{start: "1", executable: fileID{1, 2}, userNS: fileID{3, 4}, mapping: Mapping{UID: []IDRange{{0, 1000, 1}}, GID: []IDRange{{0, 1001, 1}}}}
	for i := 0; i < 5; i++ {
		next := base
		switch i {
		case 0:
			next.start = "2"
		case 1:
			next.executable.inode++
		case 2:
			next.userNS.inode++
		case 3:
			next.mapping.UID = []IDRange{{0, 1002, 1}}
		case 4:
			next.mapping.GID = []IDRange{{0, 1002, 1}}
		}
		if sameIdentity(base, next) {
			t.Fatalf("changed process identity %d accepted", i)
		}
	}
	if !sameIdentity(base, base) {
		t.Fatal("stable identity rejected")
	}
}
func TestNamespaceIdentityMap(t *testing.T) {
	for _, m := range [][]IDRange{nil, {{0, 0, 65536}}, {{0, 1000, 1}, {1, 100000, 65536}}, {{0, 0, 4294967294}}, {{0, 0, 4294967295}, {0, 0, 1}}} {
		if identityMap(m) {
			t.Fatal("noninitial namespace accepted as identity")
		}
	}
	if !identityMap([]IDRange{{0, 0, 4294967295}}) {
		t.Fatal("initial kernel map rejected")
	}
	// /proc maps must be readable from the process corresponding to this mount;
	// no configurable fake proc root is accepted by production preflight.
	proc, err := unix.Open(fmt.Sprintf("/proc/%d", os.Getpid()), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(proc)
	if _, err := procMap(proc, "missing"); err != config.ErrSecretUnavailable {
		t.Fatal("unreadable namespace map accepted")
	}
}

func TestNamespaceMappingProvenance(t *testing.T) {
	host, daemon, unrelated := fileID{1, 1}, fileID{1, 2}, fileID{1, 3}
	identity := Mapping{UID: []IDRange{{0, 0, 4294967295}}, GID: []IDRange{{0, 0, 4294967295}}}
	rootless := Mapping{UID: []IDRange{{0, 1000, 1}, {1, 100000, 65536}}, GID: []IDRange{{0, 1001, 1}, {1, 200000, 65536}}}
	for _, tc := range []struct {
		name       string
		cred       unix.Ucred
		operator   uint32
		ns, parent fileID
		maps       Mapping
		want       bool
	}{
		{"rootful identity", unix.Ucred{}, 1000, host, fileID{}, identity, true},
		{"rootful nested", unix.Ucred{}, 1000, daemon, host, identity, false},
		{"rootful remapped", unix.Ucred{}, 1000, host, fileID{}, rootless, false},
		{"rootless default", unix.Ucred{Uid: 1000, Gid: 1001}, 1000, daemon, host, rootless, true},
		{"rootless unrelated operator", unix.Ucred{Uid: 1000, Gid: 1001}, 1002, daemon, host, rootless, false},
		{"rootless nested", unix.Ucred{Uid: 1000, Gid: 1001}, 1000, daemon, unrelated, rootless, false},
		{"rootless no parent evidence", unix.Ucred{Uid: 1000, Gid: 1001}, 1000, daemon, fileID{}, rootless, false},
		{"rootless no namespace", unix.Ucred{Uid: 1000, Gid: 1001}, 1000, host, host, rootless, false},
		{"rootless mismatched uid", unix.Ucred{Uid: 1002, Gid: 1001}, 1002, daemon, host, rootless, false},
		{"rootless mismatched gid", unix.Ucred{Uid: 1000, Gid: 1002}, 1000, daemon, host, rootless, false},
		{"rootless no mapping", unix.Ucred{Uid: 1000, Gid: 1001}, 1000, daemon, host, Mapping{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if namespaceMapping(tc.cred, tc.operator, host, tc.ns, tc.parent, tc.maps) != tc.want {
				t.Fatal("namespace provenance decision differs")
			}
		})
	}
}
