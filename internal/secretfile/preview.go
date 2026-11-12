package secretfile

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/umayr/dill/internal/config"
)

func cleanPath(p string) bool {
	return strings.HasPrefix(p, "/") && p != "/" && path.Clean(p) == p && !strings.ContainsRune(p, 0)
}

const PreviewContract = "dill.alpha-secrets-preview/1"

// PreviewState is private planner evidence, never public output. Each section
// is canonical JSON; MAC framing is domain-separated and length-delimited.
type PreviewState struct{ Configuration, Live, Plan json.RawMessage }

func previewMAC(key []byte, sections ...[]byte) string {
	h := hmac.New(sha256.New, key)
	frame(h, []byte(PreviewContract))
	for _, section := range sections {
		frame(h, section)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func frame(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	h.Write(size[:])
	h.Write(b)
}

// PublishPreview writes only a keyed opaque proof. finish must be called once:
// true retains the successfully rendered plan's proof; false removes our fresh
// output on a rendering failure. Neither key/output filename enters the MAC.
func PublishPreview(req Request, result *Result, keyPath, outputPath, configPath string, state PreviewState) (finish func(bool), retErr error) {
	fail := func() (func(bool), error) { return nil, config.ErrSecretUnavailable }
	if result == nil || !result.AvailabilityChecked() || result.bound != requestID(req) || req.AuthorizationOnly {
		return fail()
	}
	next, err := authorize(req)
	if err != nil || !result.sameAuthorization(next) {
		return fail()
	}
	// Never read a source as a key. No-follow ancestry and single-link private
	// key validation also reject alternate symlink/hardlink spellings.
	keyID, err := metadataIdentity(keyPath)
	if err != nil {
		return fail()
	}
	for _, target := range next.policy.Targets {
		for _, stack := range target.Stacks {
			for _, source := range stack.Sources {
				sourceID, e := sourceMetadataIdentity(source.Path)
				if e != nil && !os.IsNotExist(e) {
					return fail()
				}
				if keyPath == source.Path || (e == nil && sourceID == keyID) || outputPath == source.Path || samePrivateEntry(outputPath, source.Path) {
					return fail()
				}
			}
		}
	}
	configAbs, err := filepath.Abs(configPath)
	if err != nil || !cleanPath(keyPath) || !cleanPath(outputPath) {
		return fail()
	}
	base := filepath.Dir(configAbs)
	if base == string(os.PathSeparator) || keyPath == configAbs || keyPath == req.PolicyPath || keyPath == req.BindingsPath || strings.HasPrefix(keyPath, base+string(os.PathSeparator)) {
		return fail()
	}
	key, err := readPrivate(keyPath, uint32(os.Geteuid()))
	if err != nil {
		return fail()
	}
	defer clear(key)
	if len(key) != 32 {
		return fail()
	}
	// Full parsed policy/binding documents are bound, including authorization
	// metadata not selected by this plan. Paths to those documents are not.
	authority, err := CanonicalPreviewJSON(struct {
		Target, Stack, Engine, Socket string
		Services                      []string
		NoDeps                        bool
		Policy                        *config.SecretPolicy
		Bindings                      *config.SecretBindings
		Mounts                        map[string][]config.ResolvedSecret
	}{req.Target, req.Stack, req.Engine, req.Socket, req.Services, req.NoDeps, next.policy, next.bindings, next.mounts})
	if err != nil {
		return fail()
	}
	sections := [][]byte{}
	for _, section := range []json.RawMessage{state.Configuration, state.Live, state.Plan} {
		if !json.Valid(section) || len(bytes.TrimSpace(section)) == 0 || bytes.TrimSpace(section)[0] != '{' {
			return fail()
		}
		b, err := CanonicalPreviewJSON(section)
		if err != nil {
			return fail()
		}
		sections = append(sections, b)
	}
	proof := previewMAC(key, authority, sections[0], sections[1], sections[2])
	data, err := json.Marshal(struct {
		Contract string `json:"contract"`
		Proof    string `json:"proof"`
	}{PreviewContract, proof})
	if err != nil {
		return fail()
	}
	return publishPrivate(outputPath, append(data, '\n'))
}

// CanonicalPreviewJSON sorts object keys, including embedded RawMessages,
// retaining evaluated JSON number spellings and semantically ordered arrays.
func CanonicalPreviewJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err = d.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
