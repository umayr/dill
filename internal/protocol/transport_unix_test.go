//go:build linux

package protocol

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func socketPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, b := os.NewFile(uintptr(fds[0]), "socket"), os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func rawFD(t *testing.T, f *os.File) int {
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
func fdFlags(t *testing.T, f *os.File) int {
	t.Helper()
	n, err := unix.FcntlInt(uintptr(rawFD(t, f)), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return n & (unix.O_ACCMODE | unix.O_NONBLOCK)
}
func shutdownWrite(t *testing.T, f *os.File) {
	t.Helper()
	if err := unix.Shutdown(rawFD(t, f), unix.SHUT_WR); err != nil {
		t.Fatal(err)
	}
}
func TestSocketSessionAndFlags(t *testing.T) {
	for _, nonblock := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocking flags", true: "nonblocking flags"}[nonblock], func(t *testing.T) {
			in, w := socketPair(t)
			out, r := socketPair(t)
			diag, dr := socketPair(t)
			files := []*os.File{in, out, diag}
			before := make([]int, 3)
			var duplicates []*os.File
			for i, f := range files {
				if nonblock {
					if err := unix.SetNonblock(rawFD(t, f), true); err != nil {
						t.Fatal(err)
					}
				}
				before[i] = fdFlags(t, f)
				d, err := unix.Dup(rawFD(t, f))
				if err != nil {
					t.Fatal(err)
				}
				dup := os.NewFile(uintptr(d), "parent-duplicate")
				defer dup.Close()
				duplicates = append(duplicates, dup)
			}
			if _, err := w.WriteString(goodRequest); err != nil {
				t.Fatal(err)
			}
			shutdownWrite(t, w)
			if code := Serve(context.Background(), CheckMode, NewTransport(in, out, diag)); code != 0 {
				t.Fatal(code)
			}
			for i, f := range files {
				if fdFlags(t, f) != before[i] || fdFlags(t, duplicates[i]) != before[i] {
					t.Fatal("changed shared flags")
				}
				duplicates[i].Close()
			}
			shutdownWrite(t, out)
			shutdownWrite(t, diag)
			b, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeStream(b); err != nil {
				t.Fatal(err, string(b))
			}
			d, _ := io.ReadAll(dr)
			if len(d) != 0 {
				t.Fatal(string(d))
			}
		})
	}
}
func TestSocketDeadlinesCancellationAndPartialWrite(t *testing.T) {
	in, _ := socketPair(t)
	out, r := socketPair(t)
	diag, _ := socketPair(t)
	p := newSocketTransport(in, out, diag)
	beforeIn, beforeOut := fdFlags(t, in), fdFlags(t, out)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, code := p.ReadRequest(ctx); code != DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatal(code, time.Since(start))
	}
	ctx2, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	start = time.Now()
	selections := 0
	if err := p.WriteFinal(ctx2, func() ([]byte, error) { selections++; return bytes.Repeat([]byte{'x'}, 2*1024*1024), nil }); err == nil || time.Since(start) > time.Second {
		t.Fatal("write deadline", err)
	}
	if selections != 1 {
		t.Fatal("rewrote final after partial progress", selections)
	}
	if fdFlags(t, in) != beforeIn || fdFlags(t, out) != beforeOut {
		t.Fatal("changed shared flags")
	}
	shutdownWrite(t, out)
	b, err := io.ReadAll(r)
	if err != nil || len(b) == 0 || len(b) >= 2*1024*1024 {
		t.Fatal("expected partial progress", len(b), err)
	}
	ctx3, cancel3 := context.WithCancel(context.Background())
	cancel3()
	if _, code := p.ReadRequest(ctx3); code != Cancelled {
		t.Fatal(code)
	}
	if err := p.Write(ctx3, []byte("x")); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestUnsupportedDescriptorsAndDiagnosticAliasing(t *testing.T) {
	in, _ := socketPair(t)
	out, r := socketPair(t)
	diag, _ := socketPair(t)
	f, err := os.CreateTemp(t.TempDir(), "unsupported")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	for _, bad := range []*os.File{f, pr, pw, nil} {
		if newSocketTransport(bad, out, diag).Supported(true) || newSocketTransport(in, bad, diag).Supported(true) {
			t.Fatal("unsupported accepted")
		}
	}
	if !newSocketTransport(f, out, diag).Supported(false) {
		t.Fatal("discovery inspected stdin")
	}
	for _, kind := range []int{unix.SOCK_DGRAM, unix.SOCK_STREAM} {
		fd, err := unix.Socket(unix.AF_UNIX, kind, 0)
		if err != nil {
			t.Fatal(err)
		}
		if socketFD(fd) {
			t.Fatal("unconnected socket accepted")
		}
		unix.Close(fd)
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if socketFD(fds[0]) {
		t.Fatal("datagram accepted")
	}
	unix.Close(fds[0])
	unix.Close(fds[1])
	tcp, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if socketFD(tcp) {
		t.Fatal("TCP accepted")
	}
	unix.Close(tcp)
	if socketFD(-1) {
		t.Fatal("invalid fd accepted")
	}
	p := newSocketTransport(in, out, out)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p.Diagnostic(ctx, InternalError)
	shutdownWrite(t, out)
	b, _ := io.ReadAll(r)
	if len(b) != 0 {
		t.Fatal("diagnostic corrupted stdout")
	}
}
func TestSocketRequestBoundsEOFAndFragments(t *testing.T) {
	for _, tt := range []struct {
		name string
		b    []byte
		eof  bool
		want Code
	}{
		{"empty", nil, true, ""},
		{"buffered EOF", []byte(goodRequest), true, ""},
		{"4096", append([]byte(goodRequest), bytes.Repeat([]byte{' '}, MaxRequest-len(goodRequest))...), true, ""},
		{"4097 held open", bytes.Repeat([]byte{'x'}, MaxRequest+1), false, RequestTooLarge},
		{"complete held open", []byte(goodRequest), false, DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in, w := socketPair(t)
			out, _ := socketPair(t)
			diag, _ := socketPair(t)
			done := make(chan error, 1)
			writerFD := rawFD(t, w)
			go func() {
				data := tt.b
				for len(data) > 0 {
					n := min(7, len(data))
					if _, err := w.Write(data[:n]); err != nil {
						done <- err
						return
					}
					data = data[n:]
				}
				if tt.eof {
					done <- unix.Shutdown(writerFD, unix.SHUT_WR)
				} else {
					done <- nil
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			b, code := newSocketTransport(in, out, diag).ReadRequest(ctx)
			w.Close() // join finite test writer even on unexpected read failure
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if code != tt.want {
				t.Fatal(code)
			}
			if tt.name == "empty" {
				if _, c := Parse(b); c != InvalidRequest {
					t.Fatal(c)
				}
			}
			if tt.name == "4096" {
				if _, c := Parse(b); c != "" {
					t.Fatal(c)
				}
			}
		})
	}
}
func TestSocketAcquisitionFinalBudget(t *testing.T) {
	in, w := socketPair(t)
	out, r := socketPair(t)
	diag, dr := socketPair(t)
	if _, err := w.WriteString(goodRequest); err != nil {
		t.Fatal(err)
	}
	// Closing only one descriptor must not masquerade as EOF while a writable
	// duplicate of the peer remains open (unlike an explicit SHUT_WR).
	dupFD, err := unix.Dup(rawFD(t, w))
	if err != nil {
		t.Fatal(err)
	}
	dup := os.NewFile(uintptr(dupFD), "held-open-peer")
	defer dup.Close()
	w.Close()
	start := time.Now()
	if code := serve(context.Background(), CheckMode, newSocketTransport(in, out, diag), fixedID, 30*time.Millisecond, 50*time.Millisecond); code != 1 || time.Since(start) > time.Second {
		t.Fatal(code)
	}
	shutdownWrite(t, out)
	shutdownWrite(t, diag)
	b, _ := io.ReadAll(r)
	d, _ := io.ReadAll(dr)
	if len(d) != 0 || !bytes.Contains(b, []byte(`"DEADLINE_EXCEEDED"`)) {
		t.Fatal(string(b), string(d))
	}
}
func TestNamedFIFORejectedEvenNonblocking(t *testing.T) {
	path := t.TempDir() + "/fifo"
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if socketFD(fd) {
		t.Fatal("FIFO accepted")
	}
}
func TestSocketSIGPIPESuppressed(t *testing.T) {
	out, peer := socketPair(t)
	p := newSocketTransport(nil, out, nil)
	if !p.Supported(false) {
		t.Fatal("socket unsupported")
	}
	peer.Close() // validation already completed; exercise sendmsg EPIPE, not fstat
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Write(ctx, []byte("x")); err == nil {
		t.Fatal("broken socket success")
	}
}
