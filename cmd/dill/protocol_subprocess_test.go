//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/umayr/dill/internal/protocol"
	"golang.org/x/sys/unix"
)

// Exercise actual main dispatch, including its early logging/env boundary.
func TestProtocolSubprocessHelper(t *testing.T) {
	if os.Getenv("DILL_PROTOCOL_TEST_HELPER") != "1" {
		return
	}
	if err := auditProtocolFixtureFDs(); err != nil {
		os.Exit(98)
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

// Check actual post-exec descriptors, not only the parent's CLOEXEC bits.
// Runtime-owned epoll/eventfd descriptors are allowed; extra fixture identities
// are not, even if the runtime reused a fixture's original numeric fd.
func protocolFixtureID(fd int) (string, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func auditProtocolFixtureFDs() error {
	ids := strings.Split(os.Getenv("DILL_PROTOCOL_TEST_FDS"), ",")
	if len(ids) == 1 && ids[0] == "" {
		return nil
	}
	if len(ids) < 6 {
		return errors.New("invalid fixture audit")
	}
	fixtures := map[string]bool{}
	for _, id := range ids {
		fixtures[id] = true
	}
	for fd := 0; fd < 3; fd++ {
		id, err := protocolFixtureID(fd)
		if err != nil || id != ids[fd] {
			return errors.New("wrong stdio mapping")
		}
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd <= 2 {
			continue
		}
		id, err := protocolFixtureID(fd)
		if err == nil && fixtures[id] {
			return errors.New("extra fixture descriptor inherited")
		}
	}
	return nil
}
func protocolChild(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	t.Cleanup(cancel)
	c := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestProtocolSubprocessHelper$", "--"}, args...)...)
	c.Env = append(os.Environ(), "DILL_PROTOCOL_TEST_HELPER=1", "DILL_PKL_PATH=/SENTINEL-unavailable-pkl", "DILL_PKL_SHA256=SENTINEL-unsupported", "DOCKER_HOST=unix:///SENTINEL-engine", "PODMAN_SOCKET=/SENTINEL-engine")
	return c
}
func childExit(err error) int {
	if err == nil {
		return 0
	}
	var e *exec.ExitError
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return -1
}
func subprocessSocket(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := os.NewFile(uintptr(fd[0]), "child-socket"), os.NewFile(uintptr(fd[1]), "parent-socket")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func subprocessFD(t *testing.T, f *os.File) int {
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
func subprocessFlags(t *testing.T, f *os.File) int {
	t.Helper()
	n, err := unix.FcntlInt(uintptr(subprocessFD(t, f)), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return n & (unix.O_ACCMODE | unix.O_NONBLOCK)
}

type childIO struct {
	in, out, diag, input, output, diagnostic *os.File
	flags                                    [3]int
}

func socketChildIO(t *testing.T, c *exec.Cmd) *childIO {
	t.Helper()
	s := &childIO{}
	s.in, s.input = subprocessSocket(t)
	s.out, s.output = subprocessSocket(t)
	s.diag, s.diagnostic = subprocessSocket(t)
	c.Stdin, c.Stdout, c.Stderr = s.in, s.out, s.diag
	for i, f := range []*os.File{s.in, s.out, s.diag} {
		s.flags[i] = subprocessFlags(t, f)
	}
	return s
}
func halfClose(t *testing.T, f *os.File) {
	t.Helper()
	if err := unix.Shutdown(subprocessFD(t, f), unix.SHUT_WR); err != nil {
		t.Fatal(err)
	}
}
func (s *childIO) request(t *testing.T, b string, eof bool) {
	t.Helper()
	if _, err := io.WriteString(s.input, b); err != nil {
		t.Fatal(err)
	}
	if eof {
		halfClose(t, s.input)
	}
}
func (s *childIO) capture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	for i, f := range []*os.File{s.in, s.out, s.diag} {
		if subprocessFlags(t, f) != s.flags[i] {
			t.Fatal("child changed parent-shared flags")
		}
	}
	halfClose(t, s.out)
	halfClose(t, s.diag)
	b, err := io.ReadAll(s.output)
	if err != nil {
		t.Fatal(err)
	}
	d, err := io.ReadAll(s.diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	return b, d
}
func captureProtocolCommand(t *testing.T, input string, fn func() error) (string, string, error) {
	t.Helper()
	s := socketChildIO(t, &exec.Cmd{})
	s.request(t, input, true)
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = s.in, s.out, s.diag
	defer func() { os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr }()
	err := fn()
	out, diag := s.capture(t)
	return string(out), string(diag), err
}
func fixedDiagnostic(c protocol.Code) string { return string(c) + ": " + c.Message() + "\n" }

const subprocessRequest = `{"protocolVersion":"dill.dev/1","operation":"protocol.check","requiredFeatures":[]}`

func TestProtocolSubprocessDescriptorInheritance(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "peer close delivers EOF", true: "peer close delivers EPIPE"}[broken], func(t *testing.T) {
			c := protocolChild(t, "protocol", "run")
			s := socketChildIO(t, c)
			// Dup does not retain CLOEXEC. Use its atomic close-on-exec variant,
			// and keep this extra peer reference live across Start as a regression.
			fd, err := unix.FcntlInt(uintptr(subprocessFD(t, s.input)), unix.F_DUPFD_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			dup := os.NewFile(uintptr(fd), "fixture-peer-duplicate")
			defer dup.Close()
			var ids []string
			for _, f := range []*os.File{s.in, s.out, s.diag, s.input, s.output, s.diagnostic, dup} {
				fd := subprocessFD(t, f)
				flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
				if err != nil || flags&unix.FD_CLOEXEC == 0 {
					t.Fatal("inheritable fixture fd", fd, flags, err)
				}
				id, err := protocolFixtureID(fd)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			c.Env = append(c.Env, "DILL_PROTOCOL_TEST_FDS="+strings.Join(ids, ","))
			s.request(t, subprocessRequest, false)
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			inputConsumed(t, s.in) // child is alive and waiting for EOF, not exited
			if broken {
				s.output.Close()
			}
			s.input.Close()
			dup.Close() // close, NOT shutdown: leaked peer would mask EOF
			exit := childExit(c.Wait())
			want := 0
			if broken {
				want = 1
			}
			if exit != want {
				t.Fatal("descriptor audit/peer closure", exit)
			}
			if broken {
				for i, f := range []*os.File{s.in, s.out, s.diag} {
					if subprocessFlags(t, f) != s.flags[i] {
						t.Fatal("child changed parent flags")
					}
				}
				halfClose(t, s.diag)
				b, err := io.ReadAll(s.diagnostic)
				if err != nil || string(b) != fixedDiagnostic(protocol.OutputFailure) {
					t.Fatal("missing EPIPE diagnostic", string(b), err)
				}
			} else {
				out, diag := s.capture(t)
				if _, err := protocol.DecodeStream(out); err != nil || len(diag) != 0 {
					t.Fatal(err, string(out), string(diag))
				}
			}
		})
	}
}
func TestProtocolMainNoLeak(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "SENTINEL-env")
	if err := os.WriteFile(envFile, []byte("DILL_PKL_PATH=/SENTINEL-would-be-loaded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		args  []string
		input string
		exit  int
		code  protocol.Code
	}{
		{"discovery", []string{"protocol"}, "", 0, ""},
		{"check", []string{"protocol", "run"}, subprocessRequest, 0, ""},
		{"empty", []string{"protocol", "run"}, "", 2, protocol.InvalidRequest},
		{"input", []string{"protocol", "run"}, `{"SENTINEL":"SENTINEL"}`, 2, protocol.InvalidRequest},
		{"4096", []string{"protocol", "run"}, subprocessRequest + strings.Repeat(" ", 4096-len(subprocessRequest)), 0, ""},
		{"4097", []string{"protocol", "run"}, strings.Repeat("x", 4097), 2, protocol.RequestTooLarge},
		{"trailing whitespace", []string{"protocol", "run"}, subprocessRequest + " \n\t", 0, ""},
		{"trailing value", []string{"protocol", "run"}, subprocessRequest + `{}`, 2, protocol.InvalidRequest},
		{"leading envfile", []string{"--env-file", envFile, "protocol", "run"}, "", 2, protocol.InvalidArguments},
		{"following envfile", []string{"protocol", "run", "--env-file", envFile}, "", 2, protocol.InvalidArguments},
		{"inline global", []string{"--env-file=" + envFile, "protocol"}, "", 2, protocol.InvalidArguments},
		{"verbosity", []string{"-vvvv", "protocol"}, "", 2, protocol.InvalidArguments},
		{"unknown argv", []string{"protocol", "--SENTINEL"}, "", 2, protocol.InvalidArguments},
		{"unknown prefix", []string{"--SENTINEL", "SENTINEL", "protocol"}, "", 2, protocol.InvalidArguments},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := protocolChild(t, tt.args...)
			s := socketChildIO(t, c)
			s.request(t, tt.input, true)
			exit := childExit(c.Run())
			out, diag := s.capture(t)
			if exit != tt.exit || len(diag) != 0 || bytes.Contains(out, []byte("SENTINEL")) {
				t.Fatalf("exit=%d out=%s stderr=%s", exit, out, diag)
			}
			r, err := protocol.DecodeStream(out)
			if err != nil {
				t.Fatal(err, string(out))
			}
			if tt.code != "" && r[0].(protocol.BootstrapResult).Errors[0].Code != tt.code {
				t.Fatal(r)
			}
		})
	}
}
func inputConsumed(t *testing.T, f *os.File) {
	t.Helper()
	request := uint(0x541b)
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, err := unix.IoctlGetInt(subprocessFD(t, f), request)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not enter acquisition")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestProtocolSubprocessInputDeadlineAndSignals(t *testing.T) {
	for _, tt := range []struct {
		name, input string
		signal      os.Signal
	}{
		{"empty held open", "", nil}, {"valid held open", subprocessRequest, nil},
		{"SIGINT", " ", syscall.SIGINT}, {"SIGTERM", " ", syscall.SIGTERM},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := protocolChild(t, "protocol", "run")
			s := socketChildIO(t, c)
			s.request(t, tt.input, false)
			start := time.Now()
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			if tt.signal != nil {
				inputConsumed(t, s.in)
				if err := c.Process.Signal(tt.signal); err != nil {
					t.Fatal(err)
				}
			}
			exit := childExit(c.Wait())
			elapsed := time.Since(start)
			out, diag := s.capture(t)
			if exit != 1 || len(diag) != 0 || elapsed > 7500*time.Millisecond {
				t.Fatalf("exit=%d elapsed=%s out=%s stderr=%s", exit, elapsed, out, diag)
			}
			want := protocol.DeadlineExceeded
			if tt.signal != nil {
				want = protocol.Cancelled
			} else if elapsed < 5*time.Second {
				t.Fatal("early deadline", elapsed)
			}
			r, err := protocol.DecodeStream(out)
			if err != nil || r[0].(protocol.BootstrapResult).Errors[0].Code != want {
				t.Fatal(err, string(out))
			}
		})
	}
}
func TestProtocolSubprocessFragmentedEOF(t *testing.T) {
	c := protocolChild(t, "protocol", "run")
	s := socketChildIO(t, c)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{subprocessRequest[:20], subprocessRequest[20:50], subprocessRequest[50:], " \n"} {
		s.request(t, part, false)
	}
	inputConsumed(t, s.in) // complete JSON is still not a complete request without EOF
	poll := []unix.PollFd{{Fd: int32(subprocessFD(t, s.output)), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 30); err != nil || n != 0 {
		t.Fatal("output before EOF", n, err)
	}
	halfClose(t, s.input)
	if exit := childExit(c.Wait()); exit != 0 {
		t.Fatal(exit)
	}
	out, diag := s.capture(t)
	if _, err := protocol.DecodeStream(out); err != nil || len(diag) != 0 {
		t.Fatal(err, string(out), string(diag))
	}
}

// Fill via per-call nonblocking sends, never poll + blocking writes (including
// in the test fixture). Shared descriptor flags remain blocking and unchanged.
func fillOutputSocket(t *testing.T, f *os.File) int {
	t.Helper()
	total := 0
	chunk := bytes.Repeat([]byte{'x'}, 4096)
	for {
		n, err := unix.SendmsgN(subprocessFD(t, f), chunk, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		total += n
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return total
		}
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 || total > 16*1024*1024 {
			t.Fatal("unexpected socket capacity", total)
		}
	}
}
func TestProtocolSubprocessBlockedAndBrokenOutput(t *testing.T) {
	for _, name := range []string{"full stdout", "full both", "broken stdout", "broken both", "full stderr", "broken stderr", "signal during full output", "input then full output"} {
		t.Run(name, func(t *testing.T) {
			args := []string{"protocol"}
			if name == "signal during full output" || name == "input then full output" {
				args = append(args, "run")
			}
			c := protocolChild(t, args...)
			s := socketChildIO(t, c)
			filled := 0
			if strings.Contains(name, "full stdout") || name == "full both" || name == "signal during full output" || name == "input then full output" {
				filled = fillOutputSocket(t, s.out)
			}
			if name == "full both" || name == "full stderr" || name == "input then full output" {
				fillOutputSocket(t, s.diag)
			}
			if name == "broken stdout" || name == "broken both" || name == "full stderr" || name == "broken stderr" {
				s.output.Close()
			}
			if name == "broken both" || name == "broken stderr" {
				s.diagnostic.Close()
			}
			if name == "input then full output" {
				s.request(t, subprocessRequest, false)
			}
			start := time.Now()
			if err := c.Start(); err != nil {
				t.Fatal(err)
			}
			if name == "signal during full output" {
				s.request(t, subprocessRequest, false)
				inputConsumed(t, s.in)
				halfClose(t, s.input)
				time.Sleep(50 * time.Millisecond) // acquisition proven active; output cannot progress
				if err := c.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			exit := childExit(c.Wait())
			elapsed := time.Since(start)
			limit := 2500 * time.Millisecond
			if name == "input then full output" {
				limit = 7500 * time.Millisecond
				if elapsed < 6*time.Second {
					t.Fatal("input/output budgets too short", elapsed)
				}
			}
			if exit != 1 || elapsed > limit {
				t.Fatal("unbounded output or SIGPIPE", exit, elapsed)
			}
			for i, f := range []*os.File{s.in, s.out, s.diag} {
				if subprocessFlags(t, f) != s.flags[i] {
					t.Fatal("changed flags")
				}
			}
			if filled > 0 {
				halfClose(t, s.out)
				b, err := io.ReadAll(s.output)
				if err != nil || len(b) != filled {
					t.Fatal("extra/partial stdout", len(b), filled, err)
				}
				if elapsed < time.Second {
					t.Fatal("output deadline too early", elapsed)
				}
			}
			if name == "broken stdout" {
				halfClose(t, s.diag)
				b, _ := io.ReadAll(s.diagnostic)
				if string(b) != fixedDiagnostic(protocol.OutputFailure) {
					t.Fatal(string(b))
				}
			}
		})
	}
}
func TestProtocolSubprocessUnsupportedTransport(t *testing.T) {
	for _, kind := range []string{"file", "blocking pipe"} {
		for _, direction := range []string{"stdin", "stdout", "stderr"} {
			t.Run(kind+" "+direction, func(t *testing.T) {
				c := protocolChild(t, "protocol", "run")
				s := socketChildIO(t, c)
				s.request(t, subprocessRequest, true)
				var bad *os.File
				if kind == "file" {
					f, err := os.CreateTemp(t.TempDir(), "unsupported")
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					bad = f
				} else {
					r, w, err := os.Pipe()
					if err != nil {
						t.Fatal(err)
					}
					defer r.Close()
					defer w.Close()
					bad = w
					if direction == "stdin" {
						bad = r
					}
				}
				if kind == "blocking pipe" {
					if err := unix.SetNonblock(subprocessFD(t, bad), false); err != nil {
						t.Fatal(err)
					}
				}
				before := subprocessFlags(t, bad)
				switch direction {
				case "stdin":
					c.Stdin = bad
				case "stdout":
					c.Stdout = bad
				case "stderr":
					c.Stderr = bad
					c.Stdout = bad
				}
				start := time.Now()
				exit := childExit(c.Run())
				if exit != 1 || time.Since(start) > 2500*time.Millisecond {
					t.Fatal(exit, time.Since(start))
				}
				if subprocessFlags(t, bad) != before {
					t.Fatal("unsupported descriptor flags changed")
				}
				out, diag := s.capture(t)
				if direction == "stdin" {
					r, err := protocol.DecodeStream(out)
					if err != nil || r[0].(protocol.BootstrapResult).Errors[0].Code != protocol.UnsupportedTransport || len(diag) != 0 {
						t.Fatal(err, string(out), string(diag))
					}
				} else if direction == "stdout" {
					if len(out) != 0 || string(diag) != fixedDiagnostic(protocol.UnsupportedTransport) {
						t.Fatal(string(out), string(diag))
					}
				} else if len(out) != 0 || len(diag) != 0 {
					t.Fatal("unsupported stderr used")
				}
				if kind == "file" {
					st, _ := bad.Stat()
					if st.Size() != 0 {
						t.Fatal("wrote unsupported regular file")
					}
				}
			})
		}
	}
}
func TestProtocolCapsBytesUnchanged(t *testing.T) {
	for engine, want := range map[string]string{
		"docker": "{\"engine\":\"docker\",\"devices\":true,\"unpublishedPorts\":true,\"networkModes\":null}\n",
		"podman": "{\"engine\":\"podman\",\"devices\":false,\"unpublishedPorts\":false,\"networkModes\":[\"host\",\"none\"]}\n",
	} {
		out, _, err := captureCommand(t, "", func() error { return run([]string{"caps", "--engine", engine}) })
		if err != nil || out != want {
			t.Fatalf("%s: %v %s", engine, err, out)
		}
	}
}
