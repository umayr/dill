package projectsnapshot

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func directorySnapshot(count int) Snapshot {
	files := make([]Entry, 0, count)
	for i := 0; i < count; i++ {
		files = append(files, entry(fmt.Sprintf("d%03d/file", i), "x"))
	}
	return Snapshot{Entrypoint: files[0].Path, Files: files}
}
func TestDirectoryLimitBoundary(t *testing.T) {
	ctx := context.Background()
	l := MaximumLimits()
	if err := Validate(ctx, directorySnapshot(255), l); err != nil {
		t.Fatalf("256 including stage root: %v", err)
	}
	if err := Validate(ctx, directorySnapshot(256), l); err == nil {
		t.Fatal("accepted 257 directories")
	}
	l.Directories = 2
	if err := Validate(ctx, directorySnapshot(1), l); err != nil {
		t.Fatalf("lower limit boundary: %v", err)
	}
	if err := Validate(ctx, directorySnapshot(2), l); err == nil {
		t.Fatal("accepted above lower limit")
	}
	l.Directories = 257
	if err := Validate(ctx, directorySnapshot(1), l); err == nil {
		t.Fatal("accepted above hard limit")
	}
	l.Directories = 0
	if err := Validate(ctx, directorySnapshot(1), l); err == nil {
		t.Fatal("accepted zero limit")
	}
}
func TestDirectoryLimitRejectsBeforeStaging(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(context.Background(), root, directorySnapshot(256), MaximumLimits()); err == nil {
		t.Fatal("accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("created resources: %v %v", entries, err)
	}
}
func TestNilContext(t *testing.T) {
	s := directorySnapshot(1)
	if err := Validate(nil, s, MaximumLimits()); err == nil {
		t.Fatal("Validate accepted nil context")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Receive(nil, root, s, MaximumLimits()); err == nil {
		t.Fatal("Receive accepted nil context")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("created resources: %v %v", entries, err)
	}
}
