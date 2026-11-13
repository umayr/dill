//go:build linux || darwin

package coordination

import (
	"encoding/binary"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// NamedPreflightScope returns PRIVATE commitment material for the retained
// directory/key identity, not path spelling or changing journal/lock contents.
// Never publish this token or use it as authentication/daemon qualification.
func (s *State) NamedPreflightScope() (string, error) {
	if s == nil || s.dir == nil || len(s.key) != 32 {
		return "", errors.New("private state identity unavailable")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(s.dir.Fd()), &stat); err != nil || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0777 != 0700 || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", errors.New("private state identity unavailable")
	}
	var identity [24]byte
	binary.BigEndian.PutUint64(identity[0:8], uint64(stat.Dev))
	binary.BigEndian.PutUint64(identity[8:16], uint64(stat.Ino))
	binary.BigEndian.PutUint64(identity[16:24], uint64(os.Geteuid()))
	return s.token("dill.named-store/1", string(identity[:])), nil
}
