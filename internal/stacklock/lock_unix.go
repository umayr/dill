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
// A deadline is mandatory; cancellation and deadline expiry return ctx.Err().
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
	path := filepath.Join(dir, hex.EncodeToString(key[:])+".lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, fmt.Errorf("open stack lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat stack lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("stack lock is not a regular file")
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
