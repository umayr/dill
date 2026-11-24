package config

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/umayr/dill/internal/imagelock"
)

// ReadImageLock reads a bounded, regular image lock without following symlinks.
// A missing path returns (nil, nil); all other failures are errors.
func ReadImageLock(path string) (*imagelock.Lock, error) {
	const limit = 4 << 20
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open image lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat image lock: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("image lock is not a regular file")
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("image lock exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read image lock: %w", err)
	}
	if len(data) > limit {
		return nil, fmt.Errorf("image lock exceeds size limit")
	}
	l, err := imagelock.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse image lock: %w", err)
	}
	return l, nil
}
