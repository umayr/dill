package projectsnapshot

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func entry(p, s string) Entry { b := []byte(s); return Entry{p, b, sha256.Sum256(b)} }
func TestValidateAdversarial(t *testing.T) {
	cases := []struct {
		name  string
		files []Entry
		main  string
	}{
		{"hash", []Entry{{Path: "x", Content: []byte("bad")}}, "x"},
		{"duplicate", []Entry{entry("x", "a"), entry("x", "b")}, "x"},
		{"case duplicate", []Entry{entry("x", "a"), entry("X", "b")}, "x"},
		{"ancestor", []Entry{entry("x", "a"), entry("x/y", "b")}, "x"},
		{"reverse ancestor", []Entry{entry("x/y", "a"), entry("X", "b")}, "X"},
		{"directory case", []Entry{entry("dir/x", "a"), entry("Dir/y", "b")}, "dir/x"},
		{"missing entry", []Entry{entry("x", "a")}, "other"},
	}
	for _, p := range []string{"../x", "/x", "a//b", "a/./b", `C:/x`, `a\b`, ".GIT/x", "NUL.txt", "com1.foo", "x.", "x ", "a?b", "é"} {
		cases = append(cases, struct {
			name  string
			files []Entry
			main  string
		}{p, []Entry{entry(p, "a")}, p})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if Validate(context.Background(), Snapshot{tc.main, tc.files}, MaximumLimits()) == nil {
				t.Fatal("accepted")
			}
		})
	}
}
func TestLimitsAndCancel(t *testing.T) {
	s := Snapshot{"x", []Entry{entry("x", "abc")}}
	l := MaximumLimits()
	l.FileBytes = 2
	if Validate(context.Background(), s, l) == nil {
		t.Fatal("file limit")
	}
	l = MaximumLimits()
	l.TotalBytes = 2
	if Validate(context.Background(), s, l) == nil {
		t.Fatal("total limit")
	}
	l = MaximumLimits()
	l.Files++
	if Validate(context.Background(), s, l) == nil {
		t.Fatal("higher limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(Validate(ctx, s, MaximumLimits()), context.Canceled) {
		t.Fatal("cancellation")
	}
}
func TestReceive(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unsupported")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	s := Snapshot{"dir/main.pkl", []Entry{entry("dir/main.pkl", "secret"), entry("other", "ok")}}
	p, err := Receive(context.Background(), root, s, MaximumLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{p, 0700}, {filepath.Join(p, "dir"), 0700}, {filepath.Join(p, "dir/main.pkl"), 0600}} {
		st, e := os.Stat(tc.path)
		if e != nil || st.Mode().Perm() != tc.mode {
			t.Fatalf("%s: %v %v", tc.path, st, e)
		}
	}
	b, err := os.ReadFile(filepath.Join(p, "dir/main.pkl"))
	if err != nil || string(b) != "secret" {
		t.Fatal("content", err)
	}
}
func TestRejectBeforeStage(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	s := Snapshot{"x", []Entry{{Path: "x", Content: []byte("bad")}}}
	if _, err := Receive(context.Background(), root, s, MaximumLimits()); err == nil {
		t.Fatal("accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("created stage", err)
	}
}
