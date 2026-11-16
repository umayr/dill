//go:build linux || darwin

package projectsnapshot

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// ErrCatalogUnknown means publication may have taken effect. Never retry or
// remove either the stage or record based on this error.
var ErrCatalogUnknown = errors.New("catalog publication outcome unknown")
var ErrStageUnknown = errors.New("stage has no catalog record")

type catalogRecord struct {
	Version        int      `json:"version"`
	ID             string   `json:"id"`
	RootDev        uint64   `json:"rootDev"`
	RootIno        uint64   `json:"rootIno"`
	StageDev       uint64   `json:"stageDev"`
	StageIno       uint64   `json:"stageIno"`
	ManifestDigest string   `json:"manifestDigest"`
	Manifest       Manifest `json:"manifest"`
}

// Catalog is a dormant local primitive, not an authorization boundary. The
// operator must own and trust both private roots, their ancestors, and all
// same-UID processes. RegisterReceived must only be called by a trusted
// receiver after durable publication; scanning a directory is not provenance.
// It does not protect against same-UID tampering, inode reuse, mounts, or changes
// after verification. Callers must coordinate all use with writers themselves.
type Catalog struct {
	mu                                         sync.Mutex
	stagePath, catalogPath                     string
	stage, catalog                             int
	stageDev, stageIno, catalogDev, catalogIno uint64
	syncFD                                     func(int) error
	rename                                     func(int, string, int, string) error
}

func privateDir(path string) (int, uint64, uint64, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, 0, 0, errors.New("root must be clean absolute path")
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, 0, 0, e
	}
	var st unix.Stat_t
	e = unix.Fstat(fd, &st)
	if e != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&077 != 0 {
		unix.Close(fd)
		return -1, 0, 0, errors.New("root must be private and owned")
	}
	return fd, uint64(st.Dev), st.Ino, nil
}

// OpenCatalog opens two existing distinct private directories. It creates none.
func OpenCatalog(stageRoot, catalogRoot string) (*Catalog, error) {
	s, sd, si, e := privateDir(stageRoot)
	if e != nil {
		return nil, e
	}
	c, cd, ci, e := privateDir(catalogRoot)
	if e != nil {
		unix.Close(s)
		return nil, e
	}
	if sd == cd && si == ci {
		unix.Close(s)
		unix.Close(c)
		return nil, errors.New("catalog and stage roots must differ")
	}
	// A previous process may have renamed a record but failed syncing the
	// catalog directory. Sync its currently visible namespace before any
	// lookup can treat such a record as durable.
	if e = unix.Fsync(c); e != nil {
		unix.Close(s)
		unix.Close(c)
		return nil, errors.New("catalog sync unavailable")
	}
	return &Catalog{stagePath: stageRoot, catalogPath: catalogRoot, stage: s, catalog: c, stageDev: sd, stageIno: si, catalogDev: cd, catalogIno: ci, syncFD: unix.Fsync, rename: renameNoReplace}, nil
}
func (c *Catalog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stage < 0 {
		return nil
	}
	e := errors.Join(unix.Close(c.stage), unix.Close(c.catalog))
	c.stage = -1
	c.catalog = -1
	return e
}
func (c *Catalog) roots() error {
	if c.stage < 0 {
		return errors.New("closed catalog")
	}
	for _, r := range []struct {
		path     string
		dev, ino uint64
	}{{c.stagePath, c.stageDev, c.stageIno}, {c.catalogPath, c.catalogDev, c.catalogIno}} {
		fd, d, i, e := privateDir(r.path)
		if e != nil {
			return e
		}
		unix.Close(fd)
		if d != r.dev || i != r.ino {
			return errors.New("root identity changed")
		}
	}
	return nil
}
func validID(id string) bool {
	if len(id) != 32 || strings.ToLower(id) != id {
		return false
	}
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16
}

const maxRecord = 8 << 20

type catalogContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r catalogContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	return r.r.Read(p)
}

// Receive stages under the exact root anchored by this catalog, not a later
// path lookup. A root replacement detected after publication is unknown and
// retains the stage. Call RegisterReceived after success to bind its manifest.
func (c *Catalog) Receive(ctx context.Context, root string, s Snapshot, limits Limits) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if root != c.stagePath {
		return "", errors.New("stage root mismatch")
	}
	if err := c.roots(); err != nil {
		return "", err
	}
	path, err := receiveWithRoot(ctx, root, c.stage, s, limits, renameNoReplace, cleanup, unix.Fstatat, identity, unix.Fsync)
	if err != nil {
		return "", err
	}
	if err := c.roots(); err != nil {
		return "", fmt.Errorf("%w: root changed after publication", ErrPublicationUnknown)
	}
	return path, nil
}

// RegisterReceived records a trusted receiver's successfully durably published
// stage. The snapshot must be the exact received snapshot. This method cannot
// independently establish provenance and must never be called on discovered dirs.
func (c *Catalog) RegisterReceived(ctx context.Context, id string, s Snapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx == nil || !validID(id) {
		return errors.New("invalid input")
	}
	if e := c.roots(); e != nil {
		return e
	}
	raw, e := CanonicalManifest(ctx, s, MaximumLimits())
	if e != nil {
		return e
	}
	var m Manifest
	if e = json.Unmarshal(raw, &m); e != nil {
		return e
	}
	dev, ino, e := c.verify(ctx, id, m, 0, 0)
	if e != nil {
		return e
	}
	digest := sha256.Sum256(raw)
	rec := catalogRecord{1, id, c.stageDev, c.stageIno, dev, ino, hex.EncodeToString(digest[:]), m}
	data, e := json.Marshal(rec)
	if e != nil {
		return e
	}
	if len(data) > maxRecord {
		return errors.New("record too large")
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	var random [16]byte
	if _, e = rand.Read(random[:]); e != nil {
		return e
	}
	temp := ".record-" + hex.EncodeToString(random[:])
	fd, e := unix.Openat(c.catalog, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), temp)
	// Temporary files are retained even on error: no cleanup of uncertain state.
	n, e := f.Write(data)
	if e == nil && n != len(data) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = c.syncFD(fd)
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = c.roots(); e != nil {
		return e
	}
	// Recheck stage immediately before publication; this is not a same-UID lock.
	if _, _, e = c.verify(ctx, id, m, dev, ino); e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = c.rename(c.catalog, temp, c.catalog, id); e != nil {
		return fmt.Errorf("%w: rename: %v", ErrCatalogUnknown, e)
	}
	if e = c.syncFD(c.catalog); e != nil {
		return fmt.Errorf("%w: sync: %v", ErrCatalogUnknown, e)
	}
	if e = ctx.Err(); e != nil {
		return fmt.Errorf("%w: %w", ErrCatalogUnknown, e)
	}
	if e = c.roots(); e != nil {
		return fmt.Errorf("%w: %v", ErrCatalogUnknown, e)
	}
	return nil
}

// Lookup returns the manifest only after record and complete stage revalidation.
func (c *Catalog) Lookup(ctx context.Context, id string) (Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lookup(ctx, id)
}
func (c *Catalog) lookup(ctx context.Context, id string) (Manifest, error) {
	var empty Manifest
	if ctx == nil || !validID(id) {
		return empty, errors.New("invalid input")
	}
	if e := c.roots(); e != nil {
		return empty, e
	}
	if e := ctx.Err(); e != nil {
		return empty, e
	}
	fd, e := unix.Openat(c.catalog, id, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(e, unix.ENOENT) {
		return empty, ErrStageUnknown
	}
	if e != nil {
		return empty, e
	}
	f := os.NewFile(uintptr(fd), id)
	defer f.Close()
	// Sync after opening the record: a concurrent publisher could rename it
	// after an earlier sync. The opened record's visible name must be included
	// in a successful catalog-directory sync before it is trusted.
	if e = c.syncFD(c.catalog); e != nil {
		return empty, errors.New("catalog sync unavailable")
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&077 != 0 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size < 0 || st.Size > maxRecord {
		return empty, errors.New("invalid record file")
	}
	data, e := io.ReadAll(catalogContextReader{ctx, io.LimitReader(f, maxRecord+1)})
	if e != nil {
		return empty, e
	}
	if len(data) > maxRecord {
		return empty, errors.New("invalid record size")
	}
	var r catalogRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return empty, e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return empty, errors.New("trailing record data")
	}
	canonical, e := json.Marshal(r)
	if e != nil || !bytes.Equal(canonical, data) || r.Version != 1 || r.ID != id || r.RootDev != c.stageDev || r.RootIno != c.stageIno {
		return empty, errors.New("invalid record")
	}
	raw, e := json.Marshal(r.Manifest)
	if e != nil {
		return empty, e
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != r.ManifestDigest {
		return empty, errors.New("manifest digest mismatch")
	}
	if _, _, e = c.verify(ctx, id, r.Manifest, r.StageDev, r.StageIno); e != nil {
		return empty, e
	}
	if e = c.roots(); e != nil {
		return empty, e
	}
	return r.Manifest, nil
}

func (c *Catalog) verify(ctx context.Context, id string, m Manifest, wantDev, wantIno uint64) (uint64, uint64, error) {
	bad := func(e error) (uint64, uint64, error) { return 0, 0, e }
	if m.Schema != "dill.snapshot.manifest.v1" || len(m.Files) == 0 || len(m.Files) > MaximumLimits().Files {
		return bad(errors.New("invalid manifest"))
	}
	files := make(map[string]ManifestFile, len(m.Files))
	dirs := map[string]bool{"": true}
	fold := map[string]string{}
	var total int64
	found := false
	last := ""
	for _, x := range m.Files {
		if e := ctx.Err(); e != nil {
			return bad(e)
		}
		if e := validPath(x.Path, MaximumLimits()); e != nil {
			return bad(e)
		}
		if x.Path <= last {
			return bad(errors.New("manifest not sorted"))
		}
		last = x.Path
		if x.Length < 0 || x.Length > MaximumLimits().FileBytes || x.Length > MaximumLimits().TotalBytes-total {
			return bad(errors.New("manifest size"))
		}
		total += x.Length
		h, e := hex.DecodeString(x.SHA256)
		if e != nil || len(h) != 32 || strings.ToLower(x.SHA256) != x.SHA256 {
			return bad(errors.New("manifest digest"))
		}
		parts := strings.Split(x.Path, "/")
		for i := 1; i < len(parts); i++ {
			p := strings.Join(parts[:i], "/")
			k := strings.ToLower(p)
			if old := fold[k]; old != "" && old != p {
				return bad(errors.New("path collision"))
			}
			if _, ok := files[p]; ok {
				return bad(errors.New("file ancestor"))
			}
			fold[k] = p
			dirs[p] = true
		}
		k := strings.ToLower(x.Path)
		if fold[k] != "" {
			return bad(errors.New("path collision"))
		}
		fold[k] = x.Path
		files[x.Path] = x
		if x.Path == m.Entrypoint {
			found = true
		}
	}
	if !found || len(dirs) > MaximumLimits().Directories {
		return bad(errors.New("manifest entrypoint or directories"))
	}
	fd, e := unix.Openat(c.stage, "snapshot-"+id, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return bad(e)
	}
	defer unix.Close(fd)
	dev, ino, e := identity(fd)
	if e != nil {
		return bad(e)
	}
	if wantDev != 0 && (dev != wantDev || ino != wantIno) {
		return bad(errors.New("stage identity changed"))
	}
	seen := 0
	seenDirs := 0
	var walk func(int, string, int) error
	walk = func(d int, prefix string, depth int) error {
		if depth > MaximumLimits().Depth {
			return errors.New("tree depth")
		}
		var st unix.Stat_t
		if err := unix.Fstat(d, &st); err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&077 != 0 || st.Uid != uint32(os.Geteuid()) {
			return errors.New("stage directory must be private and owned")
		}
		dup, e := unix.Openat(d, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		dir := os.NewFile(uintptr(dup), prefix)
		defer dir.Close()
		seenDirs++
		for {
			if e = ctx.Err(); e != nil {
				return e
			}
			names, er := dir.Readdirnames(1)
			if er == io.EOF {
				break
			}
			if er != nil {
				return er
			}
			name := names[0]
			p := name
			if prefix != "" {
				p = prefix + "/" + name
			}
			if dirs[p] {
				child, er := unix.Openat(d, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if er != nil {
					return er
				}
				er = walk(child, p, depth+1)
				unix.Close(child)
				if er != nil {
					return er
				}
				continue
			}
			x, ok := files[p]
			if !ok {
				return errors.New("unexpected stage entry")
			}
			file, er := unix.Openat(d, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
			if er != nil {
				return er
			}
			var st unix.Stat_t
			er = unix.Fstat(file, &st)
			if er != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&077 != 0 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size != x.Length {
				unix.Close(file)
				return errors.New("invalid stage file")
			}
			f := os.NewFile(uintptr(file), p)
			hash := sha256.New()
			n, er := io.Copy(hash, catalogContextReader{ctx, io.LimitReader(f, x.Length+1)})
			f.Close()
			if er != nil {
				return er
			}
			if n != x.Length || hex.EncodeToString(hash.Sum(nil)) != x.SHA256 {
				return errors.New("stage content mismatch")
			}
			seen++
		}
		return nil
	}
	if e = walk(fd, "", 0); e != nil {
		return bad(e)
	}
	if seen != len(files) || seenDirs != len(dirs) {
		return bad(errors.New("missing stage entry"))
	}
	if e = ctx.Err(); e != nil {
		return bad(e)
	}
	var st unix.Stat_t
	if e = unix.Fstatat(c.stage, "snapshot-"+id, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(st.Dev) != dev || st.Ino != ino {
		return bad(errors.New("stage identity changed"))
	}
	return dev, ino, nil
}
