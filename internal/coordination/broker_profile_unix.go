//go:build linux || darwin

package coordination

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

func ReadBrokerProfile(path string) (BrokerProfile, error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return BrokerProfile{}, ErrNamespace
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return BrokerProfile{}, ErrNamespace
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return BrokerProfile{}, ErrNamespace
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (info.Mode() != 0400 && info.Mode() != 0600) || info.Size() > 64<<10 {
		return BrokerProfile{}, ErrNamespace
	}
	var stat unix.Stat_t
	if unix.Fstat(int(f.Fd()), &stat) != nil || stat.Uid != 0 || stat.Nlink != 1 {
		return BrokerProfile{}, ErrNamespace
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil {
		return BrokerProfile{}, ErrNamespace
	}
	defer clear(data)
	return decodeBrokerProfile(data)
}
