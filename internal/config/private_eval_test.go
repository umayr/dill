package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateEvalBufferBounds(t *testing.T) {
	canceled := false
	b := &privateEvalBuffer{limit: 4, cancel: func() { canceled = true }}
	if n, err := b.Write([]byte("meta")); n != 4 || err != nil {
		t.Fatal("bounded metadata rejected")
	}
	if _, err := b.Write([]byte("synthetic-private")); err != ErrSecretUnavailable || !canceled || b.buffer.String() != "meta" {
		t.Fatal("output limit failed")
	}
}
func TestPrivatePklOutputLimitsAndCancellation(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("synthetic shell fixture requires POSIX host")
	}
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"valid", "printf '%s' '{\"config\":{}}'", true},
		{"stderr error", "printf synthetic-private >&2; exit 1", false},
		{"stdout bound", "head -c 8388609 /dev/zero", false},
		{"stderr bound", "head -c 65537 /dev/zero >&2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "pkl")
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			out, err := privatePklOutput(context.Background(), p, "synthetic-input")
			if (err == nil) != tc.ok || err != nil && err != ErrSecretUnavailable || strings.Contains(string(out), "synthetic-private") {
				t.Fatalf("unsafe evaluator result: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if _, err := privatePklOutput(ctx, "/bin/sh", "synthetic-input"); err != ErrSecretUnavailable || time.Since(started) > time.Second {
		t.Fatal("canceled evaluator did not fail promptly")
	}
}
