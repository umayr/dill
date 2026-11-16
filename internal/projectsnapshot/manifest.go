package projectsnapshot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ManifestFile describes one regular file in a canonical snapshot manifest.
type ManifestFile struct {
	Path   string `json:"path"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

// Manifest describes snapshot content, not its provenance or publication state.
// In particular, a manifest alone is not evidence that a stage was received or
// made durable, and must not be used as a catalog record.
type Manifest struct {
	Schema     string         `json:"schema"`
	Entrypoint string         `json:"entrypoint"`
	Files      []ManifestFile `json:"files"`
}

// CanonicalManifest validates a snapshot and returns deterministic JSON with
// files ordered by their exact path. The returned bytes do not alias the input.
// As with Receive, the caller must not mutate s concurrently with this call.
func CanonicalManifest(ctx context.Context, s Snapshot, limits Limits) ([]byte, error) {
	if err := Validate(ctx, s, limits); err != nil {
		return nil, err
	}
	m := Manifest{Schema: "dill.snapshot.manifest.v1", Entrypoint: s.Entrypoint, Files: make([]ManifestFile, 0, len(s.Files))}
	for _, f := range s.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.Files = append(m.Files, ManifestFile{Path: f.Path, Length: int64(len(f.Content)), SHA256: hex.EncodeToString(f.SHA256[:])})
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}
