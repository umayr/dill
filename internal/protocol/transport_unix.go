//go:build darwin || linux

package protocol

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// socketTransport borrows descriptors without registering them with the Go
// poller, changing shared OFD flags/socket options, or starting I/O workers.
// Descriptor lifetime and exclusive use are the trusted launcher's obligations.
type socketTransport struct {
	files [3]*os.File
	fd    [3]int
}

func newSocketTransport(in, out, diagnostic *os.File) *socketTransport {
	p := &socketTransport{files: [3]*os.File{in, out, diagnostic}, fd: [3]int{-1, -1, -1}}
	for i, f := range p.files {
		if f == nil {
			continue
		}
		raw, err := f.SyscallConn()
		if err != nil {
			continue
		}
		_ = raw.Control(func(fd uintptr) { p.fd[i] = int(fd) })
	}
	return p
}

func socketFD(fd int) bool {
	if !supportsSocketIO || fd < 0 {
		return false
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return false
	}
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || kind != unix.SOCK_STREAM {
		return false
	}
	local, err := unix.Getsockname(fd)
	if err != nil {
		return false
	}
	if _, ok := local.(*unix.SockaddrUnix); !ok {
		return false
	}
	// Reject unconnected/listening endpoints. Some kernels also stop reporting
	// a peer after disconnection; such endpoints fail closed as unsupported.
	peer, err := unix.Getpeername(fd)
	_, ok := peer.(*unix.SockaddrUnix)
	return err == nil && ok
}
func (p *socketTransport) Supported(read bool) bool {
	defer runtime.KeepAlive(p)
	return socketFD(p.fd[1]) && (!read || socketFD(p.fd[0]))
}

// Poll is only a readiness hint, never a proof that a blocking syscall is safe.
// Short slices bound cancellation latency without wakeup goroutines. All I/O
// below remains per-call nonblocking even with stale/spurious readiness.
func waitSocket(ctx context.Context, fd int, event int16) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		delay := 10 * time.Millisecond
		if d, ok := ctx.Deadline(); ok {
			if left := time.Until(d); left < delay {
				delay = left
			}
		}
		if delay <= 0 {
			return context.DeadlineExceeded
		}
		ms := int((delay + time.Millisecond - 1) / time.Millisecond)
		poll := []unix.PollFd{{Fd: int32(fd), Events: event}}
		n, err := unix.Poll(poll, ms)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if poll[0].Revents&unix.POLLNVAL != 0 {
			return io.ErrClosedPipe
		}
		if poll[0].Revents&(event|unix.POLLHUP|unix.POLLERR) != 0 {
			return nil // recv drains buffered data even when HUP is also present
		}
	}
}
func (p *socketTransport) ReadRequest(ctx context.Context) ([]byte, Code) {
	defer runtime.KeepAlive(p)
	if !supportsSocketIO {
		return nil, UnsupportedTransport
	}
	b := make([]byte, 0, MaxRequest+1)
	var chunk [512]byte
	for {
		if err := waitSocket(ctx, p.fd[0], unix.POLLIN); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, DeadlineExceeded
			}
			if ctx.Err() != nil {
				return nil, contextCode(ctx)
			}
			return nil, InternalError
		}
		n, _, err := unix.Recvfrom(p.fd[0], chunk[:min(len(chunk), MaxRequest+1-len(b))], unix.MSG_DONTWAIT)
		if n > 0 {
			b = append(b, chunk[:n]...)
			if len(b) > MaxRequest {
				return nil, RequestTooLarge
			}
		}
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil {
			return nil, InternalError
		}
		if n == 0 {
			return b, ""
		}
	}
}
func writeSocket(ctx context.Context, fd int, b []byte) error {
	return writeSocketSelected(ctx, fd, b, nil)
}
func writeSocketSelected(ctx context.Context, fd int, b []byte, selectFinal func() ([]byte, error)) error {
	if !supportsSocketIO {
		return errors.New("unsupported protocol transport")
	}
	for len(b) > 0 || selectFinal != nil {
		if err := waitSocket(ctx, fd, unix.POLLOUT); err != nil {
			return err
		}
		if selectFinal != nil {
			var err error
			b, err = selectFinal()
			if err != nil {
				return err
			}
			if len(b) == 0 {
				return io.ErrShortWrite
			}
		}
		// MSG_NOSIGNAL is supported by Linux sendmsg: unlike
		// SO_NOSIGPIPE or signal.Ignore it changes no shared/process state.
		n, err := unix.SendmsgN(fd, b, nil, nil, unix.MSG_DONTWAIT|unix.MSG_NOSIGNAL)
		if n > 0 {
			b = b[n:]
			selectFinal = nil // never rewrite a record after partial delivery
		}
		if err == unix.EINTR || err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
func (p *socketTransport) Write(ctx context.Context, b []byte) error {
	defer runtime.KeepAlive(p)
	return writeSocket(ctx, p.fd[1], b)
}
func (p *socketTransport) WriteFinal(ctx context.Context, choose func() ([]byte, error)) error {
	defer runtime.KeepAlive(p)
	return writeSocketSelected(ctx, p.fd[1], nil, choose)
}
func (p *socketTransport) Diagnostic(ctx context.Context, c Code) {
	defer runtime.KeepAlive(p)
	if _, ok := messages[c]; !ok {
		c = InternalError
	}
	if !socketFD(p.fd[2]) {
		return
	}
	var out, diag unix.Stat_t
	if unix.Fstat(p.fd[1], &out) == nil && unix.Fstat(p.fd[2], &diag) == nil && out.Dev == diag.Dev && out.Ino == diag.Ino {
		return
	}
	_ = writeSocket(ctx, p.fd[2], []byte(string(c)+": "+c.Message()+"\n"))
}
