package serve

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/umayr/dill/internal/projectsnapshot"
)

const uploadMax = 24 << 20

// 1000 paths of at most 512 bytes each, escaped at up to six JSON bytes per
// byte, plus per-file metadata and the entrypoint, fit below this limit.
const manifestResponseLimit = 4 << 20

var stageLimits = projectsnapshot.Limits{Files: 1000, Directories: 255, Depth: 32, PathBytes: 512, ComponentBytes: 255, FileBytes: 4 << 20, TotalBytes: 16 << 20}

type Stager struct {
	root       string
	slot       chan struct{}
	receive    func(context.Context, string, projectsnapshot.Snapshot, projectsnapshot.Limits) (string, error)
	catalog    *projectsnapshot.Catalog
	diagnostic func()
}

func NewStager(root string) (*Stager, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("invalid staging root")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, errors.New("invalid staging root")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&077 != 0 || st.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("invalid staging root")
	}
	return &Stager{root: root, slot: make(chan struct{}, 1), receive: projectsnapshot.Receive, diagnostic: func() { log.Print("snapshot staging cleanup failed; operator inspection required") }}, nil
}

// NewCataloguedStager enables durable registration for newly received stages.
// Both roots must already exist and be private, trusted, distinct directories.
// Existing stages without records remain unknown and are never adopted.
func NewCataloguedStager(root, catalogRoot string) (*Stager, error) {
	s, err := NewStager(root)
	if err != nil {
		return nil, err
	}
	catalog, err := projectsnapshot.OpenCatalog(root, catalogRoot)
	if err != nil {
		return nil, errors.New("invalid snapshot catalog")
	}
	s.catalog = catalog
	s.receive = catalog.Receive
	return s, nil
}

// WithVerifiedEntrypoint passes a digest-checked, catalogued path only to a
// trusted local consumer. It never returns the filesystem path over HTTP.
func (s *Stager) WithVerifiedEntrypoint(ctx context.Context, id, digest string, use func(string) error) error {
	if s == nil || s.catalog == nil {
		return errors.New("snapshot catalog unavailable")
	}
	return s.catalog.WithVerifiedEntrypoint(ctx, id, digest, use)
}

// Close releases catalog descriptors; it never deletes stages or records.
func (s *Stager) Close() error {
	if s == nil || s.catalog == nil {
		return nil
	}
	return s.catalog.Close()
}

// manifest returns only revalidated catalog metadata, never file contents or
// paths outside the snapshot. It shares the staging slot to bound concurrent IO.
func (s *Stager) manifest(w http.ResponseWriter, r *http.Request, id string) {
	decoded, err := hex.DecodeString(id)
	if len(id) != 32 || err != nil || len(decoded) != 16 || strings.ToLower(id) != id {
		reply(w, 400, apiError{"invalid_request", "invalid stage identifier"})
		return
	}
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		reply(w, 429, apiError{"busy", "staging busy"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	m, err := s.catalog.Lookup(ctx, id)
	if errors.Is(err, projectsnapshot.ErrStageUnknown) {
		reply(w, 404, apiError{"stage_unknown", "stage unavailable"})
		return
	}
	if err != nil {
		reply(w, 503, apiError{"stage_unavailable", "stage unavailable"})
		return
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		reply(w, 503, apiError{"stage_unavailable", "stage unavailable"})
		return
	}
	digest := sha256.Sum256(encoded)
	replyWithLimit(w, 200, struct {
		StageID  string                   `json:"stage_id"`
		Manifest projectsnapshot.Manifest `json:"manifest"`
		Digest   string                   `json:"manifest_sha256"`
	}{id, m, hex.EncodeToString(digest[:])}, manifestResponseLimit)
}

// rejectDuplicates walks the bounded JSON token stream before decoding structs.
func rejectDuplicates(d *json.Decoder, depth int) error {
	if depth > 8 {
		return errors.New("too deep")
	}
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] || (depth == 0 && key != "schema" && key != "entrypoint" && key != "files") || (depth == 2 && key != "path" && key != "bytes" && key != "length" && key != "sha256") || (depth != 0 && depth != 2) {
				return errors.New("invalid field")
			}
			seen[key] = true
			if e = rejectDuplicates(d, depth+1); e != nil {
				return e
			}
		}
		_, err = d.Token()
		return err
	case '[':
		for d.More() {
			if err := rejectDuplicates(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	default:
		return errors.New("invalid")
	}
}

type upload struct {
	Schema     *string       `json:"schema"`
	Entrypoint *string       `json:"entrypoint"`
	Files      *[]uploadFile `json:"files"`
}
type uploadFile struct {
	Path   *string `json:"path"`
	Bytes  *string `json:"bytes"`
	Length *int64  `json:"length"`
	SHA256 *string `json:"sha256"`
}

func (s *Stager) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || len(r.Header.Values("Content-Type")) != 1 || media != "application/json" || len(params) != 0 {
		reply(w, 415, apiError{"invalid_content_type", "application/json required"})
		return
	}
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		reply(w, 429, apiError{"busy", "staging busy"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadMax)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err = rejectDuplicates(d, 0); err != nil {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	if _, err = d.Token(); err != io.EOF {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	var u upload
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&u); err != nil || u.Schema == nil || *u.Schema != "dill.snapshot.v1" || u.Entrypoint == nil || u.Files == nil || len(*u.Files) > stageLimits.Files {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	snap := projectsnapshot.Snapshot{Entrypoint: *u.Entrypoint}
	for _, f := range *u.Files {
		if f.Path == nil || f.Bytes == nil || f.Length == nil || f.SHA256 == nil || len(*f.SHA256) != 64 || strings.ToLower(*f.SHA256) != *f.SHA256 {
			reply(w, 400, apiError{"invalid_request", "invalid upload"})
			return
		}
		data, e := base64.StdEncoding.Strict().DecodeString(*f.Bytes)
		if e != nil || base64.StdEncoding.EncodeToString(data) != *f.Bytes || *f.Length != int64(len(data)) {
			reply(w, 400, apiError{"invalid_request", "invalid upload"})
			return
		}
		h, e := hex.DecodeString(*f.SHA256)
		if e != nil || len(h) != sha256.Size {
			reply(w, 400, apiError{"invalid_request", "invalid upload"})
			return
		}
		var hash [32]byte
		copy(hash[:], h)
		snap.Files = append(snap.Files, projectsnapshot.Entry{Path: *f.Path, Content: data, SHA256: hash})
	}
	if err = projectsnapshot.Validate(r.Context(), snap, stageLimits); err != nil {
		reply(w, 400, apiError{"invalid_request", "invalid upload"})
		return
	}
	stage, err := s.receive(r.Context(), s.root, snap, stageLimits)
	if err != nil {
		if errors.Is(err, projectsnapshot.ErrPublicationUnknown) {
			log.Print("snapshot staging publication durability unknown; operator inspection required")
			reply(w, 500, apiError{"outcome_unknown", "staging outcome unknown; operator inspection required"})
			return
		}
		var cleanupErr *projectsnapshot.CleanupError
		if errors.As(err, &cleanupErr) {
			s.diagnostic()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reply(w, 408, apiError{"cancelled", "staging cancelled; operator inspection may be required"})
			return
		}
		reply(w, 500, apiError{"staging_failed", "staging unavailable; operator inspection may be required"})
		return
	}
	name := filepath.Base(stage)
	id := strings.TrimPrefix(name, "snapshot-")
	decoded, decodeErr := hex.DecodeString(id)
	if len(id) != 32 || decodeErr != nil || len(decoded) != 16 || strings.ToLower(id) != id || name != "snapshot-"+id {
		log.Print("snapshot staging publication result invalid; operator inspection required")
		reply(w, 500, apiError{"outcome_unknown", "staging outcome unknown; operator inspection required"})
		return
	}
	if s.catalog != nil {
		// Receive has completed all durability steps. Any registration error is
		// unknown because the stage is already published and the record may be.
		if err := s.catalog.RegisterReceived(r.Context(), id, snap); err != nil {
			log.Print("snapshot catalog registration unavailable; operator inspection required")
			reply(w, 500, apiError{"outcome_unknown", "staging outcome unknown; operator inspection required"})
			return
		}
	}
	reply(w, 201, map[string]string{"status": "staged", "stage_id": id})
}
