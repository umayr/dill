package projectsnapshot

import (
	"context"
	"strings"
	"testing"
)

func TestEachLimitBeforeStage(t *testing.T) {
	base := MaximumLimits()
	cases := []struct {
		name, path string
		tune       func(*Limits)
	}{
		{"count", "a/b", func(l *Limits) { l.Files = 1 }},
		{"depth", "a/b", func(l *Limits) { l.Depth = 1 }},
		{"path bytes", "a/b", func(l *Limits) { l.PathBytes = 2 }},
		{"component bytes", "aa/b", func(l *Limits) { l.ComponentBytes = 1 }},
		{"per file", "a/b", func(l *Limits) { l.FileBytes = 1 }},
		{"total", "a/b", func(l *Limits) { l.TotalBytes = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := base
			tc.tune(&l)
			files := []Entry{entry(tc.path, "xy")}
			if tc.name == "count" {
				files = append(files, entry("z", "q"))
			}
			if err := Validate(context.Background(), Snapshot{tc.path, files}, l); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if err := validPath(strings.Repeat("x", 256), base); err == nil {
		t.Fatal("long component accepted")
	}
}
