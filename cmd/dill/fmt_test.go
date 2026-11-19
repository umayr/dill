package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFmtFileDryRunReturnsWriteError(t *testing.T) {
	closed, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = closed
	defer func() { os.Stdout = stdout }()

	// A missing file is fine: file-mode dry-run never invokes the formatter.
	path := filepath.Join(t.TempDir(), "compose.pkl")
	err = runFmt(context.Background(), commandOptions{ConfigFile: path}, []string{"--dry-run"})
	if !errors.Is(err, os.ErrClosed) || exitCodeFor(err) != 1 {
		t.Fatalf("want closed-writer error and exit 1, got %v (exit %d)", err, exitCodeFor(err))
	}
}
