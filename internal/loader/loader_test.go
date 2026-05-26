package loader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetCache() {
	pklMu.Lock()
	cachedBin = ""
	pklMu.Unlock()
}

func TestCachePath_Default(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	p := cachePath()
	if !strings.Contains(p, "dill") {
		t.Errorf("cachePath() = %q, want path containing 'dill'", p)
	}
	if !strings.HasSuffix(p, "pkl") {
		t.Errorf("cachePath() = %q, want path ending in 'pkl'", p)
	}
}

func TestCachePath_XDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")
	p := cachePath()
	if !strings.HasPrefix(p, "/tmp/xdg-test") {
		t.Errorf("cachePath() = %q, want prefix /tmp/xdg-test", p)
	}
}

func TestReleaseURL(t *testing.T) {
	url, err := releaseURL()
	if err != nil {
		t.Skip("unsupported platform:", err)
	}
	if !strings.HasPrefix(url, "https://") {
		t.Errorf("releaseURL() = %q, want https:// prefix", url)
	}
	if !strings.Contains(url, PklVersion) {
		t.Errorf("releaseURL() = %q, want version %q in URL", url, PklVersion)
	}
}

func TestFindPkl_EnvOverride(t *testing.T) {
	// Create a fake executable in a temp dir.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "pkl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho pkl"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DILL_PKL_PATH", bin)
	resetCache()
	t.Cleanup(resetCache)

	got, err := FindPkl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Errorf("FindPkl() = %q, want %q", got, bin)
	}
}

func TestFindPkl_Cached(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "pkl-cached")
	if err := os.WriteFile(bin, []byte("#!/bin/sh"), 0755); err != nil {
		t.Fatal(err)
	}

	resetCache()
	t.Cleanup(resetCache)

	// Seed the cache directly.
	pklMu.Lock()
	cachedBin = bin
	pklMu.Unlock()

	got, err := FindPkl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Errorf("FindPkl() returned %q, want cached %q", got, bin)
	}
}
