//go:build linux || darwin

package coordination

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/umayr/dill/internal/stacklock"
	"golang.org/x/sys/unix"
)

// State pins an existing private directory. Provision this on a qualified local
// filesystem, shared by all native callers under the same operator account.
// No automatic mkdir, home/XDG fallback, NFS or container layout qualification.
// Close only after all sessions and calls have finished.
type State struct {
	dir   *os.File
	key   []byte
	fault func(string) error
}

func Open(ctx context.Context, path string) (*State, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("state directory must be absolute")
	}
	// Resolve aliases once, then pin all subsequent opens to the directory fd.
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, errors.New("state directory unavailable")
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("state directory unavailable")
	}
	dir := os.NewFile(uintptr(fd), "coordination state")
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0700 {
		dir.Close()
		return nil, errors.New("state directory must be operator-owned mode 0700")
	}
	s := &State{dir: dir}
	bootstrap := sha256.Sum256([]byte("dill-coordination-bootstrap-v1"))
	lock, err := stacklock.AcquireAt(ctx, dir, bootstrap)
	if err != nil {
		dir.Close()
		return nil, err
	}
	defer lock.Release()
	s.key, err = s.readFile("identity.key", 32)
	if errors.Is(err, os.ErrNotExist) {
		// A lost key changes EVERY stack/resource lookup. Re-keying populated
		// state would hide unresolved operations, so bootstrap only a proven
		// fresh directory containing our one known bootstrap lock.
		entries, scanErr := dir.ReadDir(2)
		fresh := scanErr == nil || errors.Is(scanErr, io.EOF)
		bootstrapName := hex.EncodeToString(bootstrap[:]) + ".lock"
		for _, entry := range entries {
			if entry.Name() != bootstrapName {
				fresh = false
			}
		}
		if !fresh || len(entries) != 1 {
			dir.Close()
			return nil, errors.New("state identity missing from populated directory; inspection required")
		}
		s.key = make([]byte, 32)
		if _, err = rand.Read(s.key); err == nil {
			err = s.replace("identity.key", s.key)
		}
	}
	if err != nil || len(s.key) != 32 {
		dir.Close()
		return nil, errors.New("state identity unavailable; inspection required")
	}
	return s, nil
}
func (s *State) Close() error { return s.dir.Close() }
func (s *State) token(kind, value string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}
func (s *State) lockKey(kind, value string) [32]byte {
	var key [32]byte
	decoded, _ := hex.DecodeString(s.token(kind, value))
	copy(key[:], decoded)
	return key
}
func (s *State) readFile(name string, limit int64) ([]byte, error) {
	fd, err := unix.Openat(int(s.dir.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size > limit {
		return nil, errors.New("unsafe state file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("state read failed")
	}
	return data, nil
}
func (s *State) checkpoint(stage string) error {
	if s.fault != nil {
		return s.fault(stage)
	}
	return nil
}

// replace deliberately leaves abandoned temporary files after process death.
// They are never treated as committed state or replay instructions.
func (s *State) replace(name string, data []byte) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return errors.New("state entropy unavailable")
	}
	temp := ".pending-" + hex.EncodeToString(nonce[:])
	fd, err := unix.Openat(int(s.dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("state temporary create failed")
	}
	f := os.NewFile(uintptr(fd), temp)
	defer func() { f.Close(); unix.Unlinkat(int(s.dir.Fd()), temp, 0) }()
	if err = s.checkpoint("created"); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return errors.New("state write failed")
	}
	if err = s.checkpoint("written"); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return errors.New("state file sync failed")
	}
	if err = s.checkpoint("synced"); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return errors.New("state close failed")
	}
	if err = unix.Renameat(int(s.dir.Fd()), temp, int(s.dir.Fd()), name); err != nil {
		return errors.New("state replace failed")
	}
	if err = s.checkpoint("renamed"); err != nil {
		return err
	}
	if err = s.dir.Sync(); err != nil {
		return errors.New("state directory sync failed")
	}
	return s.checkpoint("committed")
}
