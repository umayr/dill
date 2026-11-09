package loader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func TestVerifyDownloadedPklRejectsMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pkl")
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloadedPkl(path); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestResolveRejectsTamperedManagedCache(t *testing.T) {
	t.Setenv("DILL_PKL_PATH", "")
	t.Setenv("DILL_PKL_SHA256", "")
	t.Setenv("DILL_ALLOW_UNVERIFIED_PKL_DOWNLOAD", "")
	t.Setenv("PATH", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	path := cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "cached pkl failed verification") {
		t.Fatalf("expected cache verification error, got %v", err)
	}
}

func TestResolveRejectsLegacyDownloadOverrides(t *testing.T) {
	t.Setenv("DILL_ALLOW_UNVERIFIED_PKL_DOWNLOAD", "1")
	if _, err := resolve(context.Background()); err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("expected explicit migration error, got %v", err)
	}
}

func TestVerifyDownloadedPklAcceptsChecksum(t *testing.T) {
	content := []byte("fake")
	path := filepath.Join(t.TempDir(), "pkl")
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	url, err := releaseURL()
	if err != nil {
		t.Skip(err)
	}
	asset := filepath.Base(url)
	old := toolchain.SHA256[asset]
	toolchain.SHA256[asset] = hex.EncodeToString(sum[:])
	t.Cleanup(func() { toolchain.SHA256[asset] = old })
	if err := verifyDownloadedPkl(path); err != nil {
		t.Fatal(err)
	}
}
