package hostagent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// RevealBackend must inspect the exact immutable ID, without name or prefix
// resolution. It returns labels and environment from the same fresh inspect;
// not-found and foreign containers must not be distinguished by callers.
type RevealBackend interface {
	RevealInspect(context.Context, string) (RevealContainer, error)
}
type RevealContainer struct {
	ID     string
	Labels map[string]string
	Env    []string
}

// ErrNotFound must be returned for a missing exact container ID.
var ErrNotFound = errors.New("container not found")
var fullID = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Grant struct {
	AgentID     string `json:"agentId"`
	EngineID    string `json:"engineId"`
	Generation  uint64 `json:"generation"`
	Epoch       string `json:"epoch"`
	ContainerID string `json:"containerId"`
	SessionID   string `json:"sessionId"`
	Nonce       string `json:"nonce"`
	IssuedAt    int64  `json:"issuedAt"`
	ExpiresAt   int64  `json:"expiresAt"`
	Scope       string `json:"scope"`
	Interactive bool   `json:"interactive"`
	Signature   string `json:"signature"`
}

// RevealBytes constructs the exact signature input; it does not validate a grant.
func RevealBytes(g Grant) ([]byte, error) {
	if g.IssuedAt < 0 || g.ExpiresAt < 0 || !g.Interactive {
		return nil, errors.New("invalid grant")
	}
	var b bytes.Buffer
	b.WriteString("brine-agent-reveal/1\x00")
	for _, v := range []string{g.AgentID, g.EngineID} {
		if e := lp(&b, v); e != nil {
			return nil, e
		}
	}
	_ = binary.Write(&b, binary.BigEndian, g.Generation)
	for _, v := range []string{g.Epoch, g.ContainerID, g.SessionID, g.Nonce} {
		if e := lp(&b, v); e != nil {
			return nil, e
		}
	}
	_ = binary.Write(&b, binary.BigEndian, uint64(g.IssuedAt))
	_ = binary.Write(&b, binary.BigEndian, uint64(g.ExpiresAt))
	if e := lp(&b, g.Scope); e != nil {
		return nil, e
	}
	b.WriteByte(1)
	return b.Bytes(), nil
}
func (s *Server) reveal(w http.ResponseWriter, r *http.Request, id string, body []byte) {
	var req struct {
		ContainerID string `json:"containerId"`
		Grant       Grant  `json:"grant"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if !exactObject(body, "containerId", "grant") || !exactNestedObject(body, "grant", "agentId", "engineId", "generation", "epoch", "containerId", "sessionId", "nonce", "issuedAt", "expiresAt", "scope", "interactive", "signature") || dec.Decode(&req) != nil || !fullID.MatchString(req.ContainerID) {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	g := req.Grant
	msg, err := RevealBytes(g)
	sig, decodeErr := raw.DecodeString(g.Signature)
	if err != nil || g.AgentID != s.agentID || g.EngineID != s.c.EngineID || g.Generation != s.c.Generation || g.Epoch != s.epoch || g.ContainerID != req.ContainerID || !canonical(g.SessionID, 16) || !canonical(g.Nonce, 32) || g.Scope != "env.values" || !g.Interactive || !canonical(g.Signature, 64) || decodeErr != nil || !ed25519.Verify(s.c.RevealKey, msg, sig) {
		fail(w, id, 403, "UNAUTHORIZED", "Unauthorized.", false)
		return
	}
	// Atomically validate the trusted wall clock and consume the nonce. Never
	// evict a live nonce; a backwards wall-clock anomaly permanently fails shut.
	s.mu.Lock()
	now := time.Now()
	if !s.lastClock.IsZero() && now.UnixNano() < s.lastClock.UnixNano() {
		s.clockFailed = true
	}
	s.lastClock = now
	unix := now.Unix()
	valid := !s.clockFailed && unix >= 0 && g.IssuedAt >= 0 && g.ExpiresAt >= g.IssuedAt && g.ExpiresAt-g.IssuedAt <= 30 && g.IssuedAt <= unix+5 && g.ExpiresAt >= unix-5 && g.ExpiresAt <= int64(^uint64(0)>>1)-5
	if valid {
		for n, expiry := range s.nonces {
			if expiry < unix {
				delete(s.nonces, n)
			}
		}
		_, exists := s.nonces[g.Nonce]
		if exists || len(s.nonces) >= 10000 {
			valid = false
		} else {
			s.nonces[g.Nonce] = g.ExpiresAt + 5
		}
	}
	s.mu.Unlock()
	if !valid {
		fail(w, id, 403, "UNAUTHORIZED", "Unauthorized.", false)
		return
	}
	// Grant remains consumed even if identity, inspection, or delivery fails.
	observed, err := s.c.Identity.DaemonID(r.Context())
	if err != nil || observed == "" {
		fail(w, id, 503, "ENGINE_UNAVAILABLE", "Engine unavailable.", true)
		return
	}
	if observed != s.c.DaemonID {
		fail(w, id, 409, "BINDING_CHANGED", "Binding changed.", false)
		return
	}
	container, err := s.c.RevealBackend.RevealInspect(r.Context(), req.ContainerID)
	if errors.Is(err, ErrNotFound) {
		fail(w, id, 404, "NOT_FOUND", "Not found.", false)
		return
	}
	if err != nil {
		fail(w, id, 503, "ENGINE_UNAVAILABLE", "Engine unavailable.", true)
		return
	}
	if container.ID != req.ContainerID || container.Labels["dill.managed"] != "true" || container.Labels["dill.stack"] == "" || container.Labels["dill.service"] == "" {
		fail(w, id, 404, "NOT_FOUND", "Not found.", false)
		return
	}
	type pair struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	env := make([]pair, 0, len(container.Env))
	seen := map[string]bool{}
	if len(container.Env) > 256 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	for _, v := range container.Env {
		key, value, ok := strings.Cut(v, "=")
		if !ok || key == "" || seen[key] || !utf8.ValidString(v) || len(v) > 512<<10 {
			fail(w, id, 500, "INTERNAL", "Internal error.", false)
			return
		}
		seen[key] = true
		env = append(env, pair{key, value})
	}
	data := map[string]any{"containerId": req.ContainerID, "env": env}
	encoded, err := json.Marshal(envelope{Version: Version, RequestID: id, Data: data})
	if err != nil || len(encoded)+1 > 512<<10 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	reply(w, id, 200, data, "", "", false)
}
