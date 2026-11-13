//go:build darwin || linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Poll the invocation-owned input descriptor rather than abandoning a blocked
// reader goroutine or closing/changing the caller's stdin. Read one byte at a
// time: never consume input belonging to a subsequent confirmation. One command
// is the sole reader of its stdin while its attended prompt is active.
func readOperationConfirmation(ctx context.Context, input *os.File) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := input.SyscallConn()
	if err != nil {
		return "", errors.New("confirmation input unavailable")
	}
	var answer string
	var inputErr error
	err = raw.Control(func(fd uintptr) { answer, inputErr = pollOperationConfirmation(ctx, int(fd)) })
	if err != nil {
		return "", errors.New("confirmation input unavailable")
	}
	return answer, inputErr
}

func pollOperationConfirmation(ctx context.Context, fd int) (string, error) {
	var answer strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		wait := 50 * time.Millisecond
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining < wait {
				wait = remaining
			}
		}
		if wait <= 0 {
			return "", context.DeadlineExceeded
		}
		descriptors := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(descriptors, int((wait+time.Millisecond-1)/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return "", errors.New("confirmation input unavailable")
		}
		if n == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var b [1]byte
		n, err = unix.Read(fd, b[:])
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return "", errors.New("confirmation input unavailable")
		}
		if n == 0 {
			return answer.String(), io.EOF
		}
		if b[0] == '\n' {
			return answer.String(), nil
		}
		if answer.Len() >= 4096 {
			return "", errors.New("confirmation input too large")
		}
		answer.WriteByte(b[0])
	}
}
