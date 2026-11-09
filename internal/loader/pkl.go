// Package loader manages dill's external runtime dependencies.
//
// pkl binary resolution order:
//  1. DILL_PKL_PATH env var (explicit override)
//  2. pkl in PATH (user's existing installation)
//  3. ~/.local/share/dill/pkl (previously downloaded copy)
//  4. Download from GitHub releases and cache in #3
package loader

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/umayr/dill/internal/log"
)

//go:embed pkl-toolchain.json
var toolchainJSON []byte

var toolchain = func() struct {
	Version string            `json:"version"`
	SHA256  map[string]string `json:"sha256"`
} {
	var result struct {
		Version string            `json:"version"`
		SHA256  map[string]string `json:"sha256"`
	}
	if err := json.Unmarshal(toolchainJSON, &result); err != nil || result.Version == "" {
		panic("invalid embedded Pkl toolchain manifest")
	}
	return result
}()

// PklVersion is the pinned release used for managed downloads.
var PklVersion = toolchain.Version

var (
	pklMu     sync.Mutex
	cachedBin string
)

// FindPkl returns the path to the pkl binary, downloading and caching it on first call.
// Subsequent calls return the cached result immediately. Errors are NOT cached —
// a transient download failure allows the next call to retry.
func FindPkl(ctx context.Context) (string, error) {
	pklMu.Lock()
	if cachedBin != "" {
		bin := cachedBin
		pklMu.Unlock()
		return bin, nil
	}
	pklMu.Unlock()

	bin, err := resolve(ctx)
	if err != nil {
		return "", err
	}

	pklMu.Lock()
	cachedBin = bin
	pklMu.Unlock()
	return bin, nil
}

func resolve(ctx context.Context) (string, error) {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if value != "" && (key == "DILL_PKL_SHA256" || strings.HasPrefix(key, "DILL_PKL_SHA256_") || key == "DILL_ALLOW_UNVERIFIED_PKL_DOWNLOAD") {
			return "", fmt.Errorf("%s is no longer supported; managed downloads use pinned checksums; use DILL_PKL_PATH for an explicitly installed binary", key)
		}
	}
	// 1. Explicit env override.
	if p := os.Getenv("DILL_PKL_PATH"); p != "" {
		return p, nil
	}

	// 2. pkl already in PATH.
	if p, err := exec.LookPath("pkl"); err == nil {
		return p, nil
	}

	// 3. Previously downloaded copy.
	dst := cachePath()
	if _, err := os.Stat(dst); err == nil {
		if err := verifyDownloadedPkl(dst); err != nil {
			return "", fmt.Errorf("cached pkl failed verification: %w", err)
		}
		return dst, nil
	}

	// 4. Download.
	return downloadPkl(ctx, dst)
}

func cachePath() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	name := "pkl"
	if runtime.GOOS == "windows" {
		name = "pkl.exe"
	}
	return filepath.Join(base, "dill", PklVersion, name)
}

func downloadPkl(ctx context.Context, dst string) (string, error) {
	url, err := releaseURL()
	if err != nil {
		return "", err
	}

	logger.Info("pkl not found, downloading", "version", PklVersion, "to", dst)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(dst), ".pkl-*")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmp := f.Name()
	if err := f.Chmod(0o755); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("setting pkl permissions: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("downloading pkl: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("downloading pkl: HTTP %d from %s", resp.StatusCode, url)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf("writing pkl binary: %w", err)
	}
	f.Close()

	if err := verifyDownloadedPkl(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("installing pkl binary: %w", err)
	}

	logger.Info("pkl downloaded", "path", dst)
	return dst, nil
}

func releaseURL() (string, error) {
	var name string
	switch runtime.GOOS {
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			name = "pkl-macos-amd64"
		case "arm64":
			name = "pkl-macos-aarch64"
		}
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			name = "pkl-linux-amd64"
		case "arm64":
			name = "pkl-linux-aarch64"
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			name = "pkl-windows-amd64.exe"
		}
	}
	if name == "" {
		return "", fmt.Errorf(
			"no pre-built pkl binary for %s/%s — install pkl manually from https://pkl-lang.org",
			runtime.GOOS, runtime.GOARCH,
		)
	}
	return fmt.Sprintf(
		"https://github.com/apple/pkl/releases/download/%s/%s",
		PklVersion, name,
	), nil
}

func verifyDownloadedPkl(path string) error {
	url, err := releaseURL()
	if err != nil {
		return err
	}
	expected := toolchain.SHA256[filepath.Base(url)]
	if expected == "" {
		return fmt.Errorf("no trusted checksum for %s", filepath.Base(url))
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening downloaded pkl for verification: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing downloaded pkl: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("downloaded pkl checksum mismatch: got %s, want %s", got, expected)
	}
	return nil
}
