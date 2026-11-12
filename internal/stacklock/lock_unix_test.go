//go:build linux || darwin

package stacklock

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func deadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

func TestAcquire(t *testing.T) {
	dir := t.TempDir()
	var a, b [32]byte
	b[0] = 1
	ctx, cancel := deadline()
	defer cancel()
	first, err := Acquire(ctx, dir, a)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Acquire(ctx, dir, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Release(); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer stop()
	if _, err := Acquire(short, dir, a); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	cancelled, stopNow := context.WithCancel(ctx)
	stopNow()
	if _, err := Acquire(cancelled, dir, a); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(ctx, dir, a)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), dir, a); err == nil {
		t.Fatal("accepted unbounded wait")
	}
	if _, err := Acquire(ctx, "relative", a); err == nil {
		t.Fatal("accepted relative directory")
	}
}

func TestCancelWhileWaiting(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := deadline()
	defer cancel()
	first, err := Acquire(ctx, dir, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	waiting, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		l, err := Acquire(waiting, dir, [32]byte{})
		if l != nil {
			l.Release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("waiter returned before cancellation: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock waiter")
	}
}

func TestRejectFIFO(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0000000000000000000000000000000000000000000000000000000000000000.lock")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := deadline()
	defer cancel()
	if l, err := Acquire(ctx, dir, [32]byte{}); err == nil {
		l.Release()
		t.Fatal("accepted FIFO")
	}
}

func TestRejectSymlink(t *testing.T) {
	dir := t.TempDir()
	var key [32]byte
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "0000000000000000000000000000000000000000000000000000000000000000.lock")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := deadline()
	defer cancel()
	if _, err := Acquire(ctx, dir, key); err == nil {
		t.Fatal("accepted symlink")
	}
}

func TestProcessDeathReleasesLock(t *testing.T) {
	if os.Getenv("DILL_LOCK_CHILD") == "1" {
		ctx, cancel := deadline()
		defer cancel()
		l, err := Acquire(ctx, os.Getenv("DILL_LOCK_DIR"), [32]byte{})
		if err != nil {
			os.Exit(2)
		}
		_ = l
		os.Stdout.WriteString("locked\n")
		for {
			time.Sleep(time.Hour)
		}
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessDeathReleasesLock$")
	cmd.Env = append(os.Environ(), "DILL_LOCK_CHILD=1", "DILL_LOCK_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan error, 1)
	go func() {
		b := make([]byte, 7)
		_, err := io.ReadFull(stdout, b)
		if err == nil && string(b) != "locked\n" {
			err = errors.New("unexpected child output")
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child did not lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	_, err = Acquire(ctx, dir, [32]byte{})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active child lock: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	ctx, cancel = deadline()
	defer cancel()
	l, err := Acquire(ctx, dir, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}
