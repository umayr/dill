//go:build linux

package secretfile

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
)

func protectedTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "secretfile-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	fd, _, err := openParent(filepath.Join(dir, "probe"), uint32(os.Geteuid()))
	if err != nil {
		t.Skip("workspace ancestry is not protected; Linux CI must use a protected workspace")
	}
	unix.Close(fd)
	return dir
}
func TestReadPrivate(t *testing.T) {
	dir := protectedTestDir(t)
	p := filepath.Join(dir, "metadata")
	write := func() {
		t.Helper()
		if err := os.WriteFile(p, []byte(`{"synthetic":"metadata"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	b, err := readPrivate(p, uint32(os.Geteuid()))
	if err != nil || string(b) != `{"synthetic":"metadata"}` {
		t.Fatalf("private regular file: %v", err)
	}
	for _, mode := range []os.FileMode{0400, 0600, 0644, 0660, 0700, 0000, 04600} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		_, err := readPrivate(p, uint32(os.Geteuid()))
		if (mode == 0400 || mode == 0600) != (err == nil) {
			t.Fatalf("mode %o: %v", mode, err)
		}
	}
	os.Chmod(p, 0600)
	if _, err := readPrivate(p, uint32(os.Geteuid())+1); err != config.ErrSecretUnavailable {
		t.Fatal("wrong owner accepted")
	}
	for _, bad := range []string{"metadata", p + "/../metadata", p + "/", p + "\x00", "/"} {
		if _, err := readPrivate(bad, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
			t.Fatalf("bad path: %v", err)
		}
	}
	if err := os.Link(p, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(p, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("hardlink accepted")
	}
	os.Remove(filepath.Join(dir, "hardlink"))
	if err := os.Symlink(p, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(filepath.Join(dir, "link"), uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("symlink accepted")
	}
	if err := os.Symlink(dir, filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(filepath.Join(dir, "dirlink", "metadata"), uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("ancestor symlink accepted")
	}
	os.Chmod(dir, 0770)
	if _, err := readPrivate(p, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("writable ancestor accepted")
	}
	os.Chmod(dir, 0700)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 65537)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(p, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("oversize file accepted")
	}
	if _, err := readPrivate(dir, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("directory accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivate(fifo, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("fifo accepted")
	}
	socket := filepath.Join(dir, "socket")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err := readPrivate(socket, uint32(os.Geteuid())); err != config.ErrSecretUnavailable {
		t.Fatal("socket accepted")
	}
}
func TestSameMetadata(t *testing.T) {
	a := unix.Stat_t{Dev: 1, Ino: 2, Mode: unix.S_IFREG | 0600, Nlink: 1, Size: 5}
	if !sameMetadata(a, a) {
		t.Fatal("equal metadata differs")
	}
	for _, mutate := range []func(*unix.Stat_t){
		func(s *unix.Stat_t) { s.Dev++ }, func(s *unix.Stat_t) { s.Ino++ }, func(s *unix.Stat_t) { s.Mode++ }, func(s *unix.Stat_t) { s.Nlink++ }, func(s *unix.Stat_t) { s.Uid++ }, func(s *unix.Stat_t) { s.Gid++ }, func(s *unix.Stat_t) { s.Size++ }, func(s *unix.Stat_t) { s.Mtim.Sec++ }, func(s *unix.Stat_t) { s.Ctim.Sec++ },
	} {
		b := a
		mutate(&b)
		if sameMetadata(a, b) {
			t.Fatal("changed metadata accepted")
		}
	}
}
func TestPreflightAuthorizationOnly(t *testing.T) {
	dir := protectedTestDir(t)
	uid, gid := os.Geteuid(), os.Getegid()
	policy := fmt.Sprintf(`{"version":"dill.secret-policy/1","targets":[{"id":"lab","engine":"docker","socket":"/run/docker.sock","stacks":[{"name":"demo","sources":[{"id":"source","revision":"r1","kind":"hostFile","path":"/nonexistent-synthetic-source","uid":%d,"gid":%d,"mode":256}]}]}]}`, uid, gid)
	bindings := `{"version":"dill.secret-bindings/1","target":"lab","stack":"demo","bindings":[{"name":"db","source":"source","revision":"r1"}]}`
	pp, bp := filepath.Join(dir, "policy"), filepath.Join(dir, "bindings")
	os.WriteFile(pp, []byte(policy), 0600)
	os.WriteFile(bp, []byte(bindings), 0600)
	c := selectionConfig()
	c.Secrets = []config.SecretRequirement{{Name: "db"}}
	c.Services["db"].Secrets = []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", UID: int64(uid), GID: int64(gid), Mode: 256}}
	req := Request{Config: c, PolicyPath: pp, BindingsPath: bp, Target: "lab", Stack: "demo", Engine: "docker", Socket: "/run/docker.sock", Services: []string{"app"}, AuthorizationOnly: true}
	got, err := Preflight(context.Background(), req)
	if err != nil || len(got.mounts["db"]) != 1 {
		t.Fatalf("auth-only must not inspect absent source/socket: %v", err)
	}
	req.NoDeps = true
	got, err = Preflight(context.Background(), req)
	if err != nil || len(got.mounts) != 0 {
		t.Fatalf("no-deps: %v", err)
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Target = "other" }, func(r *Request) { r.Stack = "other" }, func(r *Request) { r.Engine = "podman" }, func(r *Request) { r.Socket = "unix:///run/docker.sock" },
	} {
		bad := req
		change(&bad)
		if _, err := Preflight(context.Background(), bad); err != config.ErrSecretUnavailable {
			t.Fatalf("scope denial: %v", err)
		}
	}
	// Shape/authorization remains whole-config even when db is unselected.
	c.Services["db"].Secrets[0].Name = "sentinel"
	if _, err := Preflight(context.Background(), req); err != config.ErrSecretUnavailable {
		t.Fatalf("unselected invalid grant: %v", err)
	}
	c.Services["db"].Secrets[0].Name = "db"
	os.WriteFile(bp, []byte(strings.Replace(bindings, "r1", "unauthorized", 1)), 0600)
	if _, err := Preflight(context.Background(), req); err != config.ErrSecretUnavailable {
		t.Fatalf("unselected unauthorized reference: %v", err)
	}
}
func TestSourceDoesNotReadContents(t *testing.T) {
	dir := protectedTestDir(t)
	p := filepath.Join(dir, "source")
	if err := os.WriteFile(p, []byte("synthetic-source-never-read"), 0400); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1000000000, 0)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	var before, after unix.Stat_t
	unix.Stat(p, &before)
	s := config.ResolvedSecret{HostPath: p, HostUID: int64(os.Geteuid()), HostGID: int64(os.Getegid()), HostMode: 0400}
	if _, err := checkSource(s); err != nil {
		t.Fatal(err)
	}
	unix.Stat(p, &after)
	if before.Atim != after.Atim || !sameMetadata(before, after) {
		t.Fatal("source metadata changed (including read atime)")
	}
}

func TestRejectExtendedACL(t *testing.T) {
	dir := protectedTestDir(t)
	p := filepath.Join(dir, "source")
	if err := os.WriteFile(p, []byte("synthetic"), 0400); err != nil {
		t.Fatal(err)
	}
	// Linux POSIX ACL xattr v2: owner, named user, group, mask, other.
	// All nonowner permissions are zero, so the mode remains exactly 0400.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, e := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, 4, ^uint32(0)}, {2, 0, uint32(os.Geteuid()) + 1}, {4, 0, ^uint32(0)}, {16, 0, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(acl[offset:], e.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], e.perm)
		binary.LittleEndian.PutUint32(acl[offset+4:], e.id)
	}
	if err := unix.Setxattr(p, "system.posix_acl_access", acl, 0); err != nil {
		if err == unix.ENOTSUP {
			t.Skip("filesystem cannot provision synthetic ACL")
		}
		t.Fatal(err)
	}
	s := config.ResolvedSecret{HostPath: p, HostUID: int64(os.Geteuid()), HostGID: int64(os.Getegid()), HostMode: 0400}
	if _, err := checkSource(s); err != config.ErrSecretUnavailable {
		t.Fatal("extended source ACL accepted")
	}
	if err := unix.Removexattr(p, "system.posix_acl_access"); err != nil {
		t.Fatal(err)
	}
	// A default ACL on an ancestor is rejected even if it does not change its
	// current mode or grant a named user any access.
	binary.LittleEndian.PutUint16(acl[6:], 7)
	if err := unix.Setxattr(dir, "system.posix_acl_default", acl, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := checkSource(s); err != config.ErrSecretUnavailable {
		t.Fatal("default ancestor ACL accepted")
	}
	if err := unix.Removexattr(dir, "system.posix_acl_default"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(dir, "system.posix_acl_access", acl, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := checkSource(s); err != config.ErrSecretUnavailable {
		t.Fatal("access ancestor ACL accepted")
	}
}

func TestResultIsBoundAndCopied(t *testing.T) {
	dir := protectedTestDir(t)
	pp, bp := filepath.Join(dir, "policy"), filepath.Join(dir, "bindings")
	policy := `{"version":"dill.secret-policy/1","targets":[{"id":"lab","engine":"docker","socket":"/run/docker.sock","stacks":[{"name":"demo","sources":[{"id":"source","revision":"r1","kind":"hostFile","path":"/synthetic-source-r1","uid":1000,"gid":1000,"mode":256},{"id":"source","revision":"r2","kind":"hostFile","path":"/synthetic-source-r2","uid":1000,"gid":1000,"mode":256}]}]}]}`
	binding := `{"version":"dill.secret-bindings/1","target":"lab","stack":"demo","bindings":[{"name":"db","source":"source","revision":"r1"}]}`
	if err := os.WriteFile(pp, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bp, []byte(binding), 0600); err != nil {
		t.Fatal(err)
	}
	c := selectionConfig()
	c.Secrets = []config.SecretRequirement{{Name: "db"}}
	c.Services["db"].Secrets = []config.SecretGrant{{Name: "db", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256}}
	req := Request{Config: c, PolicyPath: pp, BindingsPath: bp, Target: "lab", Stack: "demo", Engine: "docker", Socket: "/run/docker.sock", Services: []string{"app"}, AuthorizationOnly: true}
	result, err := Preflight(context.Background(), req)
	if err != nil || result.AvailabilityChecked() || !result.Matches(req) {
		t.Fatalf("authorization result: %v", err)
	}
	if _, err := result.Mounts(req); err != config.ErrSecretUnavailable {
		t.Fatal("authorization-only result supplied delivery inputs")
	}
	for _, zero := range []*Result{nil, {}} {
		if zero.AvailabilityChecked() || zero.Matches(req) {
			t.Fatal("zero result accepted")
		}
		if _, err := zero.Mounts(req); err != config.ErrSecretUnavailable {
			t.Fatal("zero result supplied mounts")
		}
	}
	// Synthetic evidence exercises encapsulation only, not engine qualification.
	result.runtime = &runtimeEvidence{}
	req.AuthorizationOnly = false
	mounts, err := result.Mounts(req)
	if err != nil || mounts["db"][0].Revision != "r1" {
		t.Fatal("matching request rejected")
	}
	mounts["db"][0].HostPath = "/modified-copy"
	delete(mounts, "db")
	mounts, err = result.Mounts(req)
	if err != nil || mounts["db"][0].HostPath != "/synthetic-source-r1" {
		t.Fatal("accessor exposed mutable result")
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Target = "other" }, func(r *Request) { r.Stack = "other" }, func(r *Request) { r.Engine = "podman" }, func(r *Request) { r.Socket = "/other" }, func(r *Request) { r.BindingsPath = "/other" }, func(r *Request) { r.PolicyPath = "/other" }, func(r *Request) { r.NoDeps = true }, func(r *Request) { r.Services = []string{"other"} },
	} {
		bad := req
		change(&bad)
		if result.Matches(bad) {
			t.Fatal("unrelated target/selection accepted")
		}
	}
	c.Services["db"].Secrets[0].UID = 1001
	if result.Matches(req) {
		t.Fatal("changed grant accepted")
	}
	c.Services["db"].Secrets[0].UID = 1000
	if err := os.WriteFile(bp, []byte(strings.Replace(binding, "r1", "r2", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if result.Matches(req) {
		t.Fatal("changed candidate at same path accepted")
	}
}

func TestPinnedUnixSocket(t *testing.T) {
	dir := protectedTestDir(t)
	socket := filepath.Join(dir, "socket")
	original, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	original.(*net.UnixListener).SetUnlinkOnClose(false)
	fd, err := unix.Open(socket, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// Only synthetic sockets are replaced. The descriptor must still connect to
	// the original inode, never a separately resolved replacement at the path.
	if err := os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	replacement, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	received := make(chan error, 1)
	go func() {
		original.(*net.UnixListener).SetDeadline(time.Now().Add(time.Second))
		c, e := original.Accept()
		if e == nil {
			c.Close()
		}
		received <- e
	}()
	conn, err := net.DialTimeout("unix", fmt.Sprintf("/proc/self/fd/%d", fd), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if err := <-received; err != nil {
		t.Fatalf("pinned descriptor did not connect to original socket: %v", err)
	}
}

func TestSourceMetadataWithoutReadPermission(t *testing.T) {
	const selector = "DILL_SYNTHETIC_NONOWNER_SOURCE"
	if source := os.Getenv(selector); source != "" {
		// This runs only in a disposable test subprocess. Drop all host privilege
		// before checking the synthetic root-owned file; never mutate the test host
		// process's credentials.
		if err := syscall.Setgroups(nil); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Setresgid(60000, 60000, 60000); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Setresuid(60000, 60000, 60000); err != nil {
			t.Fatal(err)
		}
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		fd, err := unix.Open(source, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err == nil {
			unix.Close(fd)
			t.Fatal("synthetic process unexpectedly has source byte-read permission")
		}
		if err != unix.EACCES {
			t.Fatalf("expected read denial: %v", err)
		}
		s := config.ResolvedSecret{HostPath: source, HostUID: 0, HostGID: 0, HostMode: 0400}
		if _, err := checkSource(s); err != nil {
			t.Fatalf("metadata-only check must not require source read permission: %v", err)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("isolated root test job needed to drop a synthetic subprocess's UID")
	}
	// Root-owned ancestry is deliberately outside /tmp and a possibly
	// operator-owned checkout, so the unprivileged child has trusted traversal.
	dir, err := os.MkdirTemp("/", "dill-secretfile-synthetic-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source")
	if err := os.WriteFile(source, []byte("synthetic-never-read"), 0400); err != nil {
		t.Fatal(err)
	}
	// A root process may have a non-root primary group; policy must match stat.
	if err := os.Chown(source, 0, 0); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSourceMetadataWithoutReadPermission$")
	cmd.Env = []string{selector + "=" + source}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic nonowner check: %v\n%s", err, out)
	}
}
