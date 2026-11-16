//go:build linux || darwin

package projectsnapshot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type created struct {
	parent int
	name   string
	fd     int
	dev    uint64
	ino    uint64
	dir    bool
}

func identity(fd int) (uint64, uint64, error) {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	return uint64(st.Dev), st.Ino, err
}
func cleanup(items []created) error {
	var errs []error
	for i := len(items) - 1; i >= 0; i-- {
		x := items[i]
		var st unix.Stat_t
		err := unix.Fstatat(x.parent, x.name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil && (uint64(st.Dev) != x.dev || st.Ino != x.ino) {
			err = fmt.Errorf("refusing to remove replaced entry %q", x.name)
		}
		if err == nil {
			flags := 0
			if x.dir {
				flags = unix.AT_REMOVEDIR
			}
			err = unix.Unlinkat(x.parent, x.name, flags)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("cleanup %q: %w", x.name, err))
		}
	}
	return errors.Join(errs...)
}

// Receive stages a fully validated snapshot under an existing trusted absolute
// root. Before returning success it syncs files, directories and the publication
// parent. The guarantees depend on filesystem and OS sync semantics, not an
// atomic snapshot of the source. A post-publication sync failure is unknown and
// retains the entire stage; earlier failures attempt identity-checked cleanup.
func Receive(ctx context.Context, root string, s Snapshot, limits Limits) (string, error) {
	return receive(ctx, root, s, limits, renameNoReplace, cleanup, unix.Fstatat, identity)
}

func receive(ctx context.Context, root string, s Snapshot, limits Limits, publish func(int, string, int, string) error, clean func([]created) error, statAt func(int, string, *unix.Stat_t, int) error, fileIdentity func(int) (uint64, uint64, error)) (string, error) {
	return receiveWithSync(ctx, root, s, limits, publish, clean, statAt, fileIdentity, unix.Fsync)
}

func receiveWithSync(ctx context.Context, root string, s Snapshot, limits Limits, publish func(int, string, int, string) error, clean func([]created) error, statAt func(int, string, *unix.Stat_t, int) error, fileIdentity func(int) (uint64, uint64, error), syncFD func(int) error) (string, error) {
	return receiveWithRoot(ctx, root, -1, s, limits, publish, clean, statAt, fileIdentity, syncFD)
}

// receiveWithRoot optionally opens a duplicate of an already anchored root.
// Callers supplying rootAnchor must hold its lifetime and coordinate writers.
func receiveWithRoot(ctx context.Context, root string, rootAnchor int, s Snapshot, limits Limits, publish func(int, string, int, string) error, clean func([]created) error, statAt func(int, string, *unix.Stat_t, int) error, fileIdentity func(int) (uint64, uint64, error), syncFD func(int) error) (result string, err error) {
	if err = Validate(ctx, s, limits); err != nil {
		return "", err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("root must be a clean absolute path")
	}
	var rootfd int
	var e error
	if rootAnchor >= 0 {
		rootfd, e = unix.Openat(rootAnchor, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	} else {
		rootfd, e = unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if e != nil {
		return "", e
	}
	defer unix.Close(rootfd)
	var rst unix.Stat_t
	if e = unix.Fstat(rootfd, &rst); e != nil {
		return "", e
	}
	if rst.Mode&077 != 0 || rst.Uid != uint32(os.Geteuid()) {
		return "", fmt.Errorf("root must be owned by current user and private")
	}
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		return "", e
	}
	suffix := hex.EncodeToString(b[:])
	name := ".in-progress-" + suffix
	finalName := "snapshot-" + suffix
	items := []created{}
	published := false
	defer func() {
		if err != nil && !published {
			if cleanupErr := clean(items); cleanupErr != nil {
				err = &CleanupError{Cause: err, Cleanup: cleanupErr}
			}
			result = ""
		}
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].dir && items[i].fd >= 0 {
				unix.Close(items[i].fd)
			}
		}
	}()
	makeDir := func(parent int, name string) (int, error) {
		if e := unix.Mkdirat(parent, name, 0700); e != nil {
			return -1, e
		}
		var st unix.Stat_t
		if e := statAt(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			return -1, &CleanupError{Cause: e, Cleanup: errors.New("created directory identity unavailable; manual inspection required")}
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR {
			return -1, &CleanupError{Cause: errors.New("created directory was replaced"), Cleanup: errors.New("created directory identity unavailable; manual inspection required")}
		}
		items = append(items, created{parent, name, -1, uint64(st.Dev), st.Ino, true})
		d, e := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return -1, e
		}
		dev, ino, e := identity(d)
		if e != nil || dev != uint64(st.Dev) || ino != st.Ino {
			unix.Close(d)
			return -1, fmt.Errorf("created directory identity changed: %w", errors.Join(e, errors.New("identity mismatch")))
		}
		items[len(items)-1].fd = d
		if e := unix.Fchmod(d, 0700); e != nil {
			return -1, e
		}
		return d, nil
	}
	fd, e := makeDir(rootfd, name)
	if e != nil {
		return "", e
	}
	dirs := map[string]int{"": fd}
	for _, entry := range s.Files {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		parts := strings.Split(entry.Path, "/")
		prefix := ""
		parent := fd
		for _, part := range parts[:len(parts)-1] {
			next := part
			if prefix != "" {
				next = prefix + "/" + part
			}
			if existing, ok := dirs[next]; ok {
				parent = existing
				prefix = next
				continue
			}
			d, e := makeDir(parent, part)
			if e != nil {
				return "", e
			}
			dirs[next] = d
			parent = d
			prefix = next
		}
		leaf := parts[len(parts)-1]
		f, e := unix.Openat(parent, leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if e != nil {
			return "", e
		}
		dv, in, e := fileIdentity(f)
		if e != nil {
			unix.Close(f)
			return "", &CleanupError{Cause: e, Cleanup: errors.New("created file identity unavailable; manual inspection required")}
		}
		items = append(items, created{parent, leaf, -1, dv, in, false})
		file := os.NewFile(uintptr(f), leaf)
		if e = unix.Fchmod(f, 0600); e != nil {
			file.Close()
			return "", e
		}
		hash := sha256.New()
		data := entry.Content
		for len(data) > 0 {
			if e = ctx.Err(); e != nil {
				file.Close()
				return "", e
			}
			n := len(data)
			if n > 32*1024 {
				n = 32 * 1024
			}
			written, we := file.Write(data[:n])
			if written > 0 {
				_, _ = hash.Write(data[:written])
				data = data[written:]
			}
			if we != nil {
				file.Close()
				return "", we
			}
			if written != n {
				file.Close()
				return "", io.ErrShortWrite
			}
		}
		if e = syncFD(f); e != nil {
			file.Close()
			return "", e
		}
		if e = file.Close(); e != nil {
			return "", e
		}
		var sum [32]byte
		copy(sum[:], hash.Sum(nil))
		if sum != entry.SHA256 {
			return "", fmt.Errorf("content changed while staging: %q", entry.Path)
		}
	}
	// Children are created after their parents; reverse creation order syncs
	// every directory bottom-up before publishing the stage root.
	for i := len(items) - 1; i >= 0; i-- {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		if items[i].dir {
			if e = syncFD(items[i].fd); e != nil {
				return "", e
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	// The in-progress name is never a completed stage. Publication must not
	// replace an existing entry, even if the randomly generated name collides.
	if e = publish(rootfd, name, rootfd, finalName); e != nil {
		// A filesystem can report an error after the rename took effect. Never
		// traverse the open stage descriptors for cleanup unless its original
		// name still identifies our stage and the final name does not. Root and
		// same-UID mutation remain outside the supported trust boundary.
		var original, final unix.Stat_t
		originalErr := statAt(rootfd, name, &original, unix.AT_SYMLINK_NOFOLLOW)
		finalErr := statAt(rootfd, finalName, &final, unix.AT_SYMLINK_NOFOLLOW)
		stage := items[0]
		originalMatches := originalErr == nil && uint64(original.Dev) == stage.dev && original.Ino == stage.ino
		finalMatches := finalErr == nil && uint64(final.Dev) == stage.dev && final.Ino == stage.ino
		finalKnown := finalErr == nil || errors.Is(finalErr, unix.ENOENT)
		if !originalMatches || finalMatches || !finalKnown {
			published = true // prevent cleanup through descriptors of an unknown tree
			return "", fmt.Errorf("%w: publication failed", ErrPublicationUnknown)
		}
		return "", e
	}
	// Once published, deleting the stage is unsafe: a consumer could already
	// have observed it. Even if the sync fails, retain it for inspection.
	published = true
	if e = syncFD(rootfd); e != nil {
		return "", fmt.Errorf("%w: %v", ErrPublicationUnknown, e)
	}
	return filepath.Join(root, finalName), nil
}
