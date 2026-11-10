package protocol

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func darwinSocketPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		unix.CloseOnExec(fd) // fixture fds, never borrowed production fds
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("inheritable fixture socket", fd, flags, err)
		}
	}
	a, b := os.NewFile(uintptr(fds[0]), "socket"), os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func darwinFD(t *testing.T, f *os.File) int {
	t.Helper()
	c, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	if err := c.Control(func(v uintptr) { fd = int(v) }); err != nil {
		t.Fatal(err)
	}
	return fd
}

// This fixture alone changes its own flags to fill a socket safely, then
// restores blocking mode BEFORE lending it to production. It is not the
// rejected poll+blocking-write strategy and not a production workaround.
func fillDarwinSocket(t *testing.T, f *os.File) {
	t.Helper()
	fd := darwinFD(t, f)
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	defer unix.SetNonblock(fd, false)
	for {
		_, err := unix.SendmsgN(fd, make([]byte, 4096), nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Do not skip the platform: assert that every former supported path fails
// closed, including full blocking sockets that reproduced the MSG_DONTWAIT
// defect. Linux runs the complete supported-socket deadline/subprocess suite.
func TestDarwinTransportFailsClosed(t *testing.T) {
	for _, kind := range []string{"socket", "full socket", "broken socket", "file", "blocking pipe"} {
		t.Run(kind, func(t *testing.T) {
			in, _ := darwinSocketPair(t)
			out, peer := darwinSocketPair(t)
			diag, _ := darwinSocketPair(t)
			if kind == "full socket" {
				fillDarwinSocket(t, out)
				fillDarwinSocket(t, diag)
			}
			if kind == "broken socket" {
				peer.Close()
			}
			if kind == "file" {
				f, err := os.CreateTemp(t.TempDir(), "unsupported")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				in, out, diag = f, f, f
			}
			if kind == "blocking pipe" {
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				in, out, diag = r, w, w
			}
			files := []*os.File{in, out, diag}
			before := make([]int, len(files))
			for i, f := range files {
				flags, err := unix.FcntlInt(uintptr(darwinFD(t, f)), unix.F_GETFL, 0)
				if err != nil {
					t.Fatal(err)
				}
				before[i] = flags
			}
			p := newSocketTransport(in, out, diag)
			if p.Supported(true) || p.Supported(false) {
				t.Fatal("Darwin transport accepted")
			}
			start := time.Now()
			for _, mode := range []Mode{DiscoveryMode, CheckMode, InvalidMode} {
				if Serve(context.Background(), mode, p) != 1 {
					t.Fatal("unsupported platform exit")
				}
			}
			if _, code := p.ReadRequest(context.Background()); code != UnsupportedTransport {
				t.Fatal(code)
			}
			if p.Write(context.Background(), []byte("x")) == nil {
				t.Fatal("direct write accepted")
			}
			p.Diagnostic(context.Background(), OutputFailure)
			if time.Since(start) > time.Second {
				t.Fatal("unsupported transport blocked")
			}
			for i, f := range files {
				flags, err := unix.FcntlInt(uintptr(darwinFD(t, f)), unix.F_GETFL, 0)
				if err != nil || flags != before[i] {
					t.Fatal("shared flags changed", flags, before[i], err)
				}
			}
		})
	}
}
