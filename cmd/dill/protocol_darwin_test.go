package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func darwinCommandSocket(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture ownership only: os.NewFile does not set close-on-exec. The
	// child must inherit only the endpoints explicitly mapped to stdio.
	for _, fd := range fds {
		unix.CloseOnExec(fd)
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatal("inheritable fixture socket", fd, flags, err)
		}
	}
	a, b := os.NewFile(uintptr(fds[0]), "child"), os.NewFile(uintptr(fds[1]), "parent")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func darwinCommandFD(t *testing.T, f *os.File) int {
	t.Helper()
	raw, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	fd := -1
	if err := raw.Control(func(v uintptr) { fd = int(v) }); err != nil {
		t.Fatal(err)
	}
	return fd
}
func captureProtocolCommand(t *testing.T, input string, fn func() error) (string, string, error) {
	t.Helper()
	in, w := darwinCommandSocket(t)
	out, r := darwinCommandSocket(t)
	diag, dr := darwinCommandSocket(t)
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatal(err)
	}
	if err := unix.Shutdown(darwinCommandFD(t, w), unix.SHUT_WR); err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = in, out, diag
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	err := fn()
	out.Close()
	diag.Close()
	b, e := io.ReadAll(r)
	if e != nil {
		t.Fatal(e)
	}
	d, e := io.ReadAll(dr)
	if e != nil {
		t.Fatal(e)
	}
	return string(b), string(d), err
}
func TestProtocolDarwinMainHelper(t *testing.T) {
	if os.Getenv("DILL_PROTOCOL_DARWIN_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"dill"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(99)
}
func TestProtocolDarwinSubprocessFailsClosed(t *testing.T) {
	for _, kind := range []string{"socket", "full socket", "broken socket", "file", "blocking pipe"} {
		t.Run(kind, func(t *testing.T) {
			in, _ := darwinCommandSocket(t)
			out, r := darwinCommandSocket(t)
			diag, dr := darwinCommandSocket(t)
			if kind == "full socket" {
				for _, f := range []*os.File{out, diag} {
					fd := darwinCommandFD(t, f)
					if err := unix.SetNonblock(fd, true); err != nil {
						t.Fatal(err)
					}
					for {
						_, err := unix.SendmsgN(fd, make([]byte, 4096), nil, nil, unix.MSG_NOSIGNAL|unix.MSG_DONTWAIT)
						if err == unix.EAGAIN {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					if err := unix.SetNonblock(fd, false); err != nil {
						t.Fatal(err)
					}
				}
			}
			if kind == "broken socket" {
				r.Close()
				dr.Close()
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
				pr, pw, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer pr.Close()
				defer pw.Close()
				in, out, diag = pr, pw, pw
				// os/exec's own File.Fd handoff makes os.Pipe blocking. Prepare
				// that launcher state before snapshotting child invariance.
				for _, f := range []*os.File{pr, pw} {
					if err := unix.SetNonblock(darwinCommandFD(t, f), false); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := make([]int, 3)
			for i, f := range []*os.File{in, out, diag} {
				flags, err := unix.FcntlInt(uintptr(darwinCommandFD(t, f)), unix.F_GETFL, 0)
				if err != nil {
					t.Fatal(err)
				}
				before[i] = flags & (unix.O_ACCMODE | unix.O_NONBLOCK)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProtocolDarwinMainHelper$", "--", "-vvvv", "--env-file=/SENTINEL", "protocol", "run")
			c.Env = append(os.Environ(), "DILL_PROTOCOL_DARWIN_HELPER=1", "DILL_PKL_PATH=/SENTINEL", "DOCKER_HOST=unix:///SENTINEL")
			c.Stdin, c.Stdout, c.Stderr = in, out, diag
			start := time.Now()
			err := c.Run()
			e, ok := err.(*exec.ExitError)
			if !ok || e.ExitCode() != 1 || time.Since(start) > 2*time.Second {
				t.Fatal("unsupported platform did not fail closed", err, time.Since(start))
			}
			for i, f := range []*os.File{in, out, diag} {
				flags, err := unix.FcntlInt(uintptr(darwinCommandFD(t, f)), unix.F_GETFL, 0)
				if err != nil || flags&(unix.O_ACCMODE|unix.O_NONBLOCK) != before[i] {
					t.Fatal("child changed flags", err)
				}
			}
			if kind == "socket" {
				out.Close()
				diag.Close()
				b, _ := io.ReadAll(r)
				d, _ := io.ReadAll(dr)
				if len(b) != 0 || len(d) != 0 {
					t.Fatal("unsupported platform emitted bytes", string(b), string(d))
				}
			}
			if kind == "file" {
				st, _ := out.Stat()
				if st.Size() != 0 {
					t.Fatal("wrote regular file")
				}
			}
		})
	}
}
