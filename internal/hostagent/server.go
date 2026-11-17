// Package hostagent implements the fail-closed subset of brine.agent.read/1.
package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const Version = "brine.agent.read/1"

var raw = base64.RawURLEncoding
var requestID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IdentitySource must query the explicitly configured engine on each call. It
// must never substitute a socket path, hostname, or cached ID for a daemon ID.
type IdentitySource interface {
	DaemonID(context.Context) (string, error)
}
type Config struct {
	EngineID                           string
	Generation                         uint64
	EngineKind, EngineSocket, DaemonID string
	PrivateKey                         ed25519.PrivateKey
	Identity                           IdentitySource
	RevealKey                          ed25519.PublicKey
	RevealBackend                      RevealBackend
	InventoryBackend                   InventoryBackend
	InspectBackend                     InspectBackend
	LogsBackend                        LogsBackend
}
type Server struct {
	c                      Config
	epoch, agentID, public string
	mu                     sync.Mutex
	nonces                 map[string]int64
	lastClock              time.Time
	clockFailed            bool
	cursorKey              [32]byte
	streams                chan struct{}
	operations             chan struct{}
}

func New(c Config) (*Server, error) {
	if len(c.PrivateKey) != ed25519.PrivateKeySize || c.Identity == nil || c.Generation == 0 || !uuid.MatchString(c.EngineID) || (c.EngineKind != "docker" && c.EngineKind != "podman") || !strings.HasPrefix(c.EngineSocket, "unix:///") || c.DaemonID == "" {
		return nil, errors.New("invalid agent configuration")
	}
	for _, s := range []string{c.EngineSocket, c.DaemonID} {
		if !utf8.ValidString(s) || len(s) > 4096 {
			return nil, errors.New("invalid agent configuration")
		}
	}
	// Reject inconsistent expanded private keys rather than advertising a key that cannot verify signatures.
	derived := ed25519.NewKeyFromSeed(c.PrivateKey[:32])
	if !bytes.Equal(derived, c.PrivateKey) {
		return nil, errors.New("invalid agent key")
	}
	if (len(c.RevealKey) != 0 && len(c.RevealKey) != ed25519.PublicKeySize) || (c.RevealBackend != nil) != (len(c.RevealKey) != 0) {
		return nil, errors.New("invalid reveal configuration")
	}
	c.RevealKey = append(ed25519.PublicKey(nil), c.RevealKey...)
	c.PrivateKey = append(ed25519.PrivateKey(nil), c.PrivateKey...)
	epoch := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, epoch); err != nil {
		return nil, err
	}
	pub := c.PrivateKey.Public().(ed25519.PublicKey)
	h := sha256.Sum256(pub)
	s := &Server{c: c, epoch: raw.EncodeToString(epoch), agentID: hex.EncodeToString(h[:]), public: raw.EncodeToString(pub), nonces: make(map[string]int64), streams: make(chan struct{}, 2), operations: make(chan struct{}, 16)}
	if _, err := io.ReadFull(rand.Reader, s.cursorKey[:]); err != nil {
		return nil, err
	}
	return s, nil
}
func lp(b *bytes.Buffer, s string) error {
	if !utf8.ValidString(s) || len(s) > 4096 {
		return errors.New("invalid field")
	}
	_ = binary.Write(b, binary.BigEndian, uint32(len(s)))
	b.WriteString(s)
	return nil
}

// SessionBytes returns the exact domain-separated bytes signed for a session.
func SessionBytes(nonce, agentID, public, engineID string, generation uint64, kind, socket, daemon, epoch string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("brine-agent-session/1\x00")
	for _, s := range []string{nonce, agentID, public, engineID} {
		if err := lp(&b, s); err != nil {
			return nil, err
		}
	}
	_ = binary.Write(&b, binary.BigEndian, generation)
	for _, s := range []string{kind, socket, daemon, epoch} {
		if err := lp(&b, s); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}
func canonical(s string, n int) bool {
	b, e := raw.DecodeString(s)
	return e == nil && len(b) == n && raw.EncodeToString(b) == s
}

type envelope struct {
	Version   string     `json:"version"`
	RequestID string     `json:"requestId"`
	Data      any        `json:"data,omitempty"`
	Error     *wireError `json:"error,omitempty"`
}
type wireError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func reply(w http.ResponseWriter, id string, status int, data any, code, msg string, retry bool) {
	e := envelope{Version: Version, RequestID: id, Data: data}
	if code != "" {
		e.Error = &wireError{code, msg, retry}
	}
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	body = append(body, '\n')
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetWriteDeadline(time.Now().Add(5*time.Second)) == nil
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// A known length avoids a deferred chunk terminator after our flush.
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err == nil && deadlineSet {
		// Flush the entire fixed-length response while the deadline is active.
		// Do not clear it if the writer cannot flush or if flushing fails.
		if controller.Flush() == nil {
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}
}
func fail(w http.ResponseWriter, id string, status int, code, msg string, retry bool) {
	reply(w, id, status, nil, code, msg, retry)
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/logs" {
		select {
		case s.operations <- struct{}{}:
			defer func() { <-s.operations }()
		default:
			fail(w, "", 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
			return
		}
	}
	id := r.Header.Get("X-Request-Id")
	if len(r.Header.Values("X-Request-Id")) != 1 || !requestID.MatchString(id) {
		fail(w, "", 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.Path != "/v1/session" && r.URL.Path != "/v1/inventory" && r.URL.Path != "/v1/inspect" && r.URL.Path != "/v1/logs" && r.URL.Path != "/v1/reveal" {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	if len(r.Header.Values("X-Brine-Binding")) != 1 || r.Header.Get("X-Brine-Binding") != strconv.FormatUint(s.c.Generation, 10) {
		fail(w, id, 409, "BINDING_CHANGED", "Binding changed.", false)
		return
	}
	if r.URL.Path != "/v1/session" && (len(r.Header.Values("X-Agent-Epoch")) != 1 || r.Header.Get("X-Agent-Epoch") != s.epoch) {
		fail(w, id, 403, "UNAUTHORIZED", "Unauthorized.", false)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32769))
	if err != nil {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	if len(body) > 32768 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	if !utf8.Valid(body) || !uniqueJSON(body) {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	if r.URL.Path == "/v1/logs" && s.c.LogsBackend != nil && s.c.InventoryBackend != nil {
		s.logs(w, r, id, body)
		return
	}
	if r.URL.Path == "/v1/inspect" && s.c.InspectBackend != nil {
		s.inspect(w, r, id, body)
		return
	}
	if r.URL.Path == "/v1/inventory" && s.c.InventoryBackend != nil {
		s.inventory(w, r, id, body)
		return
	}
	if r.URL.Path == "/v1/reveal" && s.c.RevealBackend != nil {
		s.reveal(w, r, id, body)
		return
	}
	if r.URL.Path != "/v1/session" {
		fail(w, id, 501, "UNSUPPORTED", "Unsupported.", false)
		return
	}
	var req struct {
		Nonce string `json:"nonce"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if !exactObject(body, "nonce") || d.Decode(&req) != nil || !canonical(req.Nonce, 32) {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	observed, err := s.c.Identity.DaemonID(r.Context())
	if err != nil || observed == "" {
		fail(w, id, 503, "ENGINE_UNAVAILABLE", "Engine unavailable.", true)
		return
	}
	if observed != s.c.DaemonID {
		fail(w, id, 409, "BINDING_CHANGED", "Binding changed.", false)
		return
	}
	msg, _ := SessionBytes(req.Nonce, s.agentID, s.public, s.c.EngineID, s.c.Generation, s.c.EngineKind, s.c.EngineSocket, s.c.DaemonID, s.epoch)
	reply(w, id, 200, map[string]any{"agentId": s.agentID, "agentPublicKey": s.public, "engineId": s.c.EngineID, "generation": s.c.Generation, "engineKind": s.c.EngineKind, "engineSocket": s.c.EngineSocket, "daemonId": s.c.DaemonID, "epoch": s.epoch, "nonce": req.Nonce, "signature": raw.EncodeToString(ed25519.Sign(s.c.PrivateKey, msg))}, "", "", false)
}

func exactObject(body []byte, names ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || len(fields) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return false
		}
	}
	return true
}
func exactNestedObject(body []byte, name string, names ...string) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(body, &fields) == nil && exactObject(fields[name], names...)
}

// uniqueJSON rejects duplicate members at every depth and trailing values.
func uniqueJSON(b []byte) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if walk(d) != nil {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}
func walk(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
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
			s, ok := k.(string)
			if !ok || seen[s] {
				return errors.New("duplicate")
			}
			seen[s] = true
			if e = walk(d); e != nil {
				return e
			}
		}
		t, e = d.Token()
		if e != nil || t != json.Delim('}') {
			return errors.New("object")
		}
	case '[':
		for d.More() {
			if e = walk(d); e != nil {
				return e
			}
		}
		t, e = d.Token()
		if e != nil || t != json.Delim(']') {
			return errors.New("array")
		}
	default:
		return errors.New("delimiter")
	}
	return nil
}
