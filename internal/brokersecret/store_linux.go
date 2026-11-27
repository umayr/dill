//go:build linux

package brokersecret

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

const maxGenerations = 256
const maxRuntimeBytes = 32 << 20

// Store is a single-writer rootful Linux runtime directory. Its lock and key
// must survive restarts. It is private execution state, not source history.
type Store struct {
	mu       sync.Mutex
	root     *os.Root
	path     string
	identity os.FileInfo
	lock     *os.File
	key      func() []byte
	now      func() time.Time
}

func rootInfo(path string) (os.FileInfo, error) {
	if os.Geteuid() != 0 {
		return nil, ErrUnsupported
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path || !filepath.IsAbs(path) {
		return nil, ErrUnavailable
	}
	i, e := os.Lstat(path)
	if e != nil || i.Mode() != os.ModeDir|0700 {
		return nil, ErrUnavailable
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 {
		return nil, ErrUnavailable
	}
	return i, nil
}
func private(info os.FileInfo, mode os.FileMode, uid, gid uint32) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uid && s.Gid == gid && s.Nlink == 1
}

// Provision creates only a new runtime directory. Existing directories, even
// empty ones, are never reinitialized as a recovery action.
func Provision(path string) error {
	if os.Geteuid() != 0 {
		return ErrUnsupported
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalid
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil || parent != filepath.Dir(path) {
		return ErrUnavailable
	}
	if os.Mkdir(path, 0700) != nil {
		return ErrUnavailable
	}
	if os.Chmod(path, 0700) != nil {
		return ErrUnavailable
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return ErrUnavailable
	}
	defer root.Close()
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return ErrUnavailable
	}
	if writeNew(root, "key", key[:], 0600, 0, 0) != nil || writeNew(root, "lock", nil, 0600, 0, 0) != nil {
		return ErrUnavailable
	}
	if syncRoot(root) != nil {
		return ErrUnavailable
	}
	p, e := os.Open(parent)
	if e != nil {
		return ErrUnavailable
	}
	defer p.Close()
	if p.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}
func Open(path string) (*Store, error) {
	i, e := rootInfo(path)
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, ErrUnavailable
	}
	fail := func() (*Store, error) { root.Close(); return nil, ErrUnavailable }
	lock, e := root.OpenFile("lock", os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return fail()
	}
	li, e := lock.Stat()
	if e != nil || !private(li, 0600, 0, 0) || syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		return fail()
	}
	key, e := readPrivate(root, "key", 32, 0600, 0, 0)
	if e != nil || len(key) != 32 {
		lock.Close()
		return fail()
	}
	return &Store{root: root, path: path, identity: i, lock: lock, key: func() []byte { return key }, now: time.Now}, nil
}
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	if s.key != nil {
		clear(s.key())
	}
	s.key = nil
	s.root.Close()
	s.root = nil
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
	return nil
}
func (s *Store) ready() bool {
	if s == nil || s.root == nil || s.key == nil || len(s.key()) != 32 {
		return false
	}
	i, e := rootInfo(s.path)
	if e != nil || !os.SameFile(i, s.identity) {
		return false
	}
	key, e := readPrivate(s.root, "key", 32, 0600, 0, 0)
	if e != nil || !hmac.Equal(key, s.key()) {
		clear(key)
		return false
	}
	clear(key)
	current, e := s.root.Lstat("lock")
	opened, openErr := s.lock.Stat()
	return e == nil && openErr == nil && private(current, 0600, 0, 0) && os.SameFile(current, opened)
}
func syncRoot(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return ErrUnavailable
	}
	defer f.Close()
	if f.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}
func writeNew(root *os.Root, name string, data []byte, mode os.FileMode, uid, gid int) error {
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return ErrUnavailable
	}
	defer f.Close()
	if _, e = f.Write(data); e != nil || f.Chown(uid, gid) != nil || f.Chmod(mode) != nil || f.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}
func readPrivate(root *os.Root, name string, limit int, mode os.FileMode, uid, gid uint32) ([]byte, error) {
	i, e := root.Lstat(name)
	if e != nil || !private(i, mode, uid, gid) || i.Size() > int64(limit) {
		return nil, ErrUnavailable
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(i, actual) || !private(actual, mode, uid, gid) {
		return nil, ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) > limit {
		return nil, ErrUnavailable
	}
	return b, nil
}
func (s *Store) capacity() error {
	entries, e := os.ReadDir(s.path)
	if e != nil {
		return ErrUnavailable
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if entry.Name() == "key" || entry.Name() == "lock" {
			continue
		}
		if !entry.IsDir() {
			return ErrUnavailable
		}
		count++
		e = filepath.WalkDir(filepath.Join(s.path, entry.Name()), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.Type()&os.ModeSymlink != 0 {
				return ErrUnavailable
			}
			if !d.IsDir() {
				i, e := d.Info()
				if e != nil || !i.Mode().IsRegular() {
					return ErrUnavailable
				}
				total += i.Size()
			}
			if total > maxRuntimeBytes {
				return ErrCapacity
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	if count >= maxGenerations || total+MaxEntries*MaxValue > maxRuntimeBytes {
		return ErrCapacity
	}
	return nil
}
func (s *Store) Prepare(e Envelope, expected Scope, grants []Grant) (Review, error) {
	if err := e.Validate(expected, grants); err != nil {
		return Review{}, err
	}
	if s == nil {
		return Review{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready() {
		return Review{}, ErrUnavailable
	}
	if err := s.capacity(); err != nil {
		return Review{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Review{}, ErrUnavailable
	}
	id := hex.EncodeToString(nonce[:])
	if s.root.Mkdir(id, 0700) != nil {
		return Review{}, ErrUnavailable
	}
	dir, err := s.root.OpenRoot(id)
	if err != nil {
		return Review{}, ErrUnavailable
	}
	defer dir.Close()
	now := s.now().UTC()
	m := manifest{Contract: Version, Review: Review{Handle: id, Scope: expected, ExpiresAt: now.Add(5 * time.Minute)}, CreatedAt: now}
	values := map[string][]byte{}
	for _, item := range e.Items {
		values[item.Name] = item.bytes()
	}
	ordered := append([]Grant(nil), grants...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Service+"/"+ordered[i].Name < ordered[j].Service+"/"+ordered[j].Name
	})
	dirs := map[string]bool{}
	for _, g := range ordered {
		if !dirs[g.Service] {
			if dir.Mkdir(g.Service, 0700) != nil {
				return Review{}, ErrUnavailable
			}
			dirs[g.Service] = true
		}
		name := g.Service + "/" + g.Name
		if writeNew(dir, name, values[g.Name], os.FileMode(g.Mode), int(g.UID), int(g.GID)) != nil {
			return Review{}, ErrUnavailable
		}
		m.Files = append(m.Files, storedFile{g, name})
	}
	for service := range dirs {
		child, e := dir.OpenRoot(service)
		if e != nil {
			return Review{}, ErrUnavailable
		}
		err := syncRoot(child)
		child.Close()
		if err != nil {
			return Review{}, err
		}
	}
	mac, err := s.mac(dir, m)
	if err != nil {
		return Review{}, err
	}
	m.MAC = mac
	raw, err := json.Marshal(m)
	if err != nil || writeNew(dir, "manifest.json", raw, 0600, 0, 0) != nil || syncRoot(dir) != nil || syncRoot(s.root) != nil {
		return Review{}, ErrUnavailable
	}
	return m.Review, nil
}
func (s *Store) mac(dir *os.Root, m manifest) (string, error) {
	m.MAC = ""
	raw, e := json.Marshal(m)
	if e != nil {
		return "", ErrUnavailable
	}
	h := hmac.New(sha256.New, s.key())
	h.Write([]byte("dill.brokersecret.manifest/1\x00"))
	h.Write(raw)
	for _, file := range m.Files {
		g := file.Grant
		b, e := readPrivate(dir, file.File, MaxValue, os.FileMode(g.Mode), uint32(g.UID), uint32(g.GID))
		if e != nil {
			return "", ErrUnavailable
		}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(file.File)))
		h.Write(length[:])
		h.Write([]byte(file.File))
		binary.BigEndian.PutUint64(length[:], uint64(len(b)))
		h.Write(length[:])
		h.Write(b)
		clear(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *Store) load(id string, expected Scope) (manifest, *os.Root, error) {
	if !s.ready() || !validID(id) || !expected.Valid() {
		return manifest{}, nil, ErrUnavailable
	}
	i, e := s.root.Lstat(id)
	if e != nil || i.Mode() != os.ModeDir|0700 {
		return manifest{}, nil, ErrUnavailable
	}
	dir, e := s.root.OpenRoot(id)
	if e != nil {
		return manifest{}, nil, ErrUnavailable
	}
	fail := func() (manifest, *os.Root, error) { dir.Close(); return manifest{}, nil, ErrUnavailable }
	raw, e := readPrivate(dir, "manifest.json", 256<<10, 0600, 0, 0)
	if e != nil || !uniqueJSON(raw) {
		return fail()
	}
	var m manifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || !validManifestKind(m) || m.Review.Handle != id || m.Review.Scope != expected || len(m.Files) > MaxEntries || m.CreatedAt.IsZero() || m.Review.ExpiresAt.Sub(m.CreatedAt) != 5*time.Minute || s.now().Before(m.CreatedAt) {
		return fail()
	}
	for _, f := range m.Files {
		if f.File != f.Grant.Service+"/"+f.Grant.Name {
			return fail()
		}
	}
	mac, e := s.mac(dir, m)
	if e != nil || !hmac.Equal([]byte(mac), []byte(m.MAC)) {
		return fail()
	}
	if !m.Review.Active && !s.now().Before(m.Review.ExpiresAt) {
		dir.Close()
		return manifest{}, nil, ErrExpired
	}
	return m, dir, nil
}

// Resolve is private executor input for planning. Apply must call Activate
// before its first engine mutation, then reconcile unknown outcomes by ID.
func (s *Store) Resolve(id string, expected Scope) (*Materialized, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, dir, e := s.load(id, expected)
	if e != nil {
		return nil, e
	}
	dir.Close()
	return s.materialized(m), nil
}
func (s *Store) materialized(m manifest) *Materialized {
	r := &Materialized{store: s, review: m.Review, retirement: m.Retirement}
	for _, f := range m.Files {
		g := f.Grant
		r.mounts = append(r.mounts, Mount{Service: g.Service, Name: g.Name, HostPath: filepath.Join(s.path, m.Review.Handle, f.File), Target: g.Target, UID: g.UID, GID: g.GID, Mode: g.Mode})
	}
	return r
}

// Activate is conservative: even an uncertain write leaves material retained.
// This method is not deployment authorization; caller rechecks its reviewed
// operation, ownership, target identities and host-local grants independently.
func (s *Store) Activate(id string, expected Scope) (*Materialized, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, dir, e := s.load(id, expected)
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	if !m.Review.Active {
		m.Review.Active = true
		m.MAC, e = s.mac(dir, m)
		if e != nil {
			return nil, e
		}
		raw, e := json.Marshal(m)
		if e != nil || writeNew(dir, "manifest.next", raw, 0600, 0, 0) != nil || dir.Rename("manifest.next", "manifest.json") != nil || syncRoot(dir) != nil {
			return nil, ErrUnavailable
		}
	}
	return s.materialized(m), nil
}
