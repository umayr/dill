package hostagent

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// InventoryBackend must bound List to at most 1001 entries and return an
// error rather than hide results. Ownership is rechecked via InventoryInspect.
// ErrLimit signals that a backend cannot prove a complete result within its budget.
var ErrLimit = errors.New("limit exceeded")

type InventoryBackend interface {
	InventoryList(context.Context) ([]InventoryContainer, error)
	InventoryInspect(context.Context, string) (InventoryContainer, error)
}
type InventoryContainer struct {
	ID, Name, State, Image string
	Labels                 map[string]string
}
type inventoryItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Stack   string `json:"stack"`
	Service string `json:"service"`
	State   string `json:"state"`
	Image   string `json:"image"`
}
type inventoryCursor struct {
	Epoch      string `json:"epoch"`
	Engine     string `json:"engine"`
	Generation uint64 `json:"generation"`
	Last       string `json:"last"`
	Expires    int64  `json:"expires"`
}

func (s *Server) encodeCursor(last string, now time.Time) string {
	b, _ := json.Marshal(inventoryCursor{s.epoch, s.c.EngineID, s.c.Generation, last, now.Unix() + 60})
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(b)
	return raw.EncodeToString(b) + "." + raw.EncodeToString(mac.Sum(nil))
}
func (s *Server) decodeCursor(token string, now time.Time) (string, error) {
	if len(token) > 2048 {
		return "", errors.New("invalid cursor")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid cursor")
	}
	b, e := raw.DecodeString(parts[0])
	if e != nil || raw.EncodeToString(b) != parts[0] {
		return "", errors.New("invalid cursor")
	}
	tag, e := raw.DecodeString(parts[1])
	if e != nil || len(tag) != 32 || raw.EncodeToString(tag) != parts[1] {
		return "", errors.New("invalid cursor")
	}
	mac := hmac.New(sha256.New, s.cursorKey[:])
	mac.Write(b)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return "", errors.New("invalid cursor")
	}
	var c inventoryCursor
	if json.Unmarshal(b, &c) != nil || c.Epoch != s.epoch || c.Engine != s.c.EngineID || c.Generation != s.c.Generation || !fullID.MatchString(c.Last) || c.Expires < now.Unix() || c.Expires > now.Unix()+60 {
		return "", errors.New("invalid cursor")
	}
	return c.Last, nil
}
func (s *Server) inventory(w http.ResponseWriter, r *http.Request, id string, body []byte) {
	var req struct {
		Cursor *string `json:"cursor"`
		Limit  int     `json:"limit"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if !exactObject(body, "cursor", "limit") || dec.Decode(&req) != nil || req.Limit < 1 || req.Limit > 100 {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	now := time.Now().UTC()
	last := ""
	if req.Cursor != nil {
		var e error
		last, e = s.decodeCursor(*req.Cursor, now)
		if e != nil {
			fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	daemon, e := s.c.Identity.DaemonID(ctx)
	if e != nil || daemon == "" {
		inventoryError(w, id, ctx)
		return
	}
	if daemon != s.c.DaemonID {
		fail(w, id, 409, "BINDING_CHANGED", "Binding changed.", false)
		return
	}
	list, e := s.c.InventoryBackend.InventoryList(ctx)
	if errors.Is(e, ErrLimit) {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	if e != nil {
		inventoryError(w, id, ctx)
		return
	}
	if len(list) > 1000 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	items := make([]inventoryItem, 0, req.Limit)
	var next any
	prev := ""
	for _, candidate := range list {
		if !fullID.MatchString(candidate.ID) || candidate.ID == prev {
			fail(w, id, 500, "INTERNAL", "Internal error.", false)
			return
		}
		prev = candidate.ID
		if candidate.ID <= last {
			continue
		}
		// Filter first, then independently inspect the immutable ID before exposing it.
		if candidate.Labels["dill.managed"] != "true" || candidate.Labels["dill.stack"] == "" || candidate.Labels["dill.service"] == "" {
			continue
		}
		c, e := s.c.InventoryBackend.InventoryInspect(ctx, candidate.ID)
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			inventoryError(w, id, ctx)
			return
		}
		if c.ID != candidate.ID || c.Labels["dill.managed"] != "true" || c.Labels["dill.stack"] == "" || c.Labels["dill.service"] == "" {
			continue
		}
		for _, v := range []string{c.Name, c.State, c.Image, c.Labels["dill.stack"], c.Labels["dill.service"]} {
			if !utf8.ValidString(v) || len(v) > 8192 {
				fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
				return
			}
		}
		if len(items) == req.Limit {
			next = s.encodeCursor(items[len(items)-1].ID, now)
			break
		}
		items = append(items, inventoryItem{c.ID, c.Name, c.Labels["dill.stack"], c.Labels["dill.service"], c.State, c.Image})
	}
	if ctx.Err() != nil {
		inventoryError(w, id, ctx)
		return
	}
	data := map[string]any{"items": items, "nextCursor": next, "observedAt": now.Format(time.RFC3339Nano)}
	b, e := json.Marshal(envelope{Version: Version, RequestID: id, Data: data})
	if e != nil || len(b)+1 > 512<<10 {
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	reply(w, id, 200, data, "", "", false)
}
func inventoryError(w http.ResponseWriter, id string, ctx context.Context) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fail(w, id, 504, "DEADLINE", "Deadline exceeded.", true)
	} else {
		fail(w, id, 503, "ENGINE_UNAVAILABLE", "Engine unavailable.", true)
	}
}
