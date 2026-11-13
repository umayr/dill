//go:build linux || darwin

// Package stacklock provides an unwired, local advisory locking primitive.
// It does not define stack identity or protect any Dill operation by itself.
package stacklock

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Lock holds an advisory lock on a persistent file. It must not be copied.
type Lock struct{ file *os.File }

// Acquire waits for key in dir. dir must be an existing absolute, trusted,
// shared local directory, inaccessible to untrusted writers. All participants
// must derive exactly the same key and never unlink or replace lock files.
// A deadline is mandatory; while waiting for the lock, cancellation and
// deadline expiry return ctx.Err(). The deadline does not bound filesystem
// calls, which may block or fail independently of the context.
// This primitive does not qualify filesystems, directories, or identities.
func Acquire(ctx context.Context, dir string, key [32]byte) (*Lock, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("stack lock requires a deadline")
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("stack lock directory must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return acquire(ctx, func(name string) (int, error) {
		return unix.Open(filepath.Join(dir, name), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	}, key)
}

// AcquireAt is Acquire relative to an already opened, trusted directory. The
// caller owns dir and must keep it open until AcquireAt returns. Relative opens
// keep coordination on the same directory inode if its pathname is replaced.
func AcquireAt(ctx context.Context, dir *os.File, key [32]byte) (*Lock, error) {
	if dir == nil {
		return nil, errors.New("missing lock directory")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("stack lock requires a deadline")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return acquire(ctx, func(name string) (int, error) {
		return unix.Openat(int(dir.Fd()), name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	}, key)
}

func acquire(ctx context.Context, open func(string) (int, error), key [32]byte) (*Lock, error) {
	name := hex.EncodeToString(key[:]) + ".lock"
	fd, err := open(name)
	if err != nil {
		return nil, fmt.Errorf("open stack lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat stack lock: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		f.Close()
		return nil, errors.New("stack lock is not a private operator-owned regular file")
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if err := ctx.Err(); err != nil {
				unix.Flock(fd, unix.LOCK_UN)
				f.Close()
				return nil, err
			}
			return &Lock{file: f}, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN && err != unix.EINTR {
			f.Close()
			return nil, fmt.Errorf("acquire stack lock: %w", err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Release unlocks without removing the persistent lock file. Do not call
// concurrently or use the lock after release.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return errors.New("stack lock already released")
	}
	f := l.file
	l.file = nil
	err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}
