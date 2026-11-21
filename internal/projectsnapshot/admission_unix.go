//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	stagingBytes  = 512 << 20
	stagingStages = 64
)

type admissionBudget struct {
	bytes  int64
	stages int
}

// admit reserves the candidate's space by holding an exclusive lock on the
// already-open root until the caller closes rootfd. Independent opens of the
// same directory contend, even across processes; no lock file can be left behind.
func admit(ctx context.Context, rootfd int, root unix.Stat_t, s Snapshot, budget admissionBudget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Flock(rootfd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return ErrStagingBusy
		}
		return ErrStagingInventory // never fall back to an unlocked receive
	}
	if budget.stages <= 0 || budget.bytes < 0 {
		return ErrStagingInventory
	}
	budget.stages--
	for _, f := range s.Files {
		n := int64(len(f.Content))
		if n > budget.bytes {
			return ErrStagingCapacity
		}
		budget.bytes -= n
	}
	limits := MaximumLimits()
	return scanAdmissionDir(ctx, rootfd, ".", root, func(fd int, name string) error {
		id, ok := strings.CutPrefix(name, "snapshot-")
		if !ok {
			id, ok = strings.CutPrefix(name, ".in-progress-")
		}
		if !ok || !validID(id) {
			return ErrStagingInventory
		}
		st, err := admissionStat(fd, name, root)
		if err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrStagingInventory
		}
		if budget.stages == 0 {
			return ErrStagingCapacity
		}
		budget.stages--

		// A crash can leave a partial tree. Charge its observed bytes and a
		// whole stage slot, whether or not a catalog record exists.
		files, dirs := 0, 0
		var walk func(int, string, unix.Stat_t, int) error
		walk = func(parent int, dir string, want unix.Stat_t, depth int) error {
			dirs++
			if dirs > limits.Directories || depth >= limits.Depth {
				return ErrStagingInventory
			}
			return scanAdmissionDir(ctx, parent, dir, want, func(fd int, name string) error {
				st, err := admissionStat(fd, name, root)
				if err != nil {
					return err
				}
				switch st.Mode & unix.S_IFMT {
				case unix.S_IFDIR:
					return walk(fd, name, st, depth+1)
				case unix.S_IFREG:
					files++
					if files > limits.Files || st.Nlink != 1 || st.Size < 0 {
						return ErrStagingInventory
					}
					if st.Size > budget.bytes {
						return ErrStagingCapacity
					}
					budget.bytes -= st.Size
					return nil
				default:
					return ErrStagingInventory
				}
			})
		}
		return walk(fd, name, st, 0)
	})
}

func admissionStat(fd int, name string, root unix.Stat_t) (unix.Stat_t, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil || st.Dev != root.Dev || st.Uid != root.Uid || st.Mode&077 != 0 {
		return st, ErrStagingInventory
	}
	return st, nil
}

// Enumerate in bounded batches using only anchored, no-follow opens. Verify
// directory identity across stat/open; this is not protection from arbitrary
// same-UID writers, which are outside the cooperative lock's trust boundary.
func scanAdmissionDir(ctx context.Context, parent int, name string, want unix.Stat_t, visit func(int, string) error) error {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrStagingInventory
	}
	dir := os.NewFile(uintptr(fd), "staging inventory")
	defer dir.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Dev != want.Dev || st.Ino != want.Ino || st.Uid != want.Uid || st.Mode&077 != 0 {
		return ErrStagingInventory
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		names, err := dir.Readdirnames(32)
		if err != nil && err != io.EOF {
			return ErrStagingInventory
		}
		for _, name := range names {
			if e := ctx.Err(); e != nil {
				return e
			}
			if e := visit(fd, name); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return ctx.Err()
		}
	}
}
