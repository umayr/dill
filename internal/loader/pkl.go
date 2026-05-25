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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/umayr/dill/internal/log"
)

const (
	// PklVersion is the pkl release fetched when no system pkl is found.
	// Corresponds to the binary shipped at https://github.com/apple/pkl/releases.
	PklVersion = "0.31.1"
)

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
	return filepath.Join(base, "dill", name)
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

	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
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
