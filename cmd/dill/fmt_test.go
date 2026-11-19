package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFmtFileReportsConfirmationWriteErrorAfterFormatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.pkl")
	if err := os.WriteFile(path, []byte("answer=42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Give the formatter a valid descriptor while making confirmation writes fail.
	readOnly, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	stdout := os.Stdout
	os.Stdout = readOnly
	defer func() { os.Stdout = stdout }()

	err = runFmt(context.Background(), commandOptions{ConfigFile: path}, nil)
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "answer = 42\n" {
		t.Fatalf("formatting did not complete: file=%q read error=%v fmt error=%v", got, readErr, err)
	}
	var writeErr *os.PathError
	if !errors.As(err, &writeErr) || writeErr.Op != "write" || exitCodeFor(err) != 1 ||
		!strings.Contains(err.Error(), "formatting completed, but writing confirmation failed") {
		t.Fatalf("want confirmation write error after formatting and exit 1, got %v (exit %d)", err, exitCodeFor(err))
	}
}

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
