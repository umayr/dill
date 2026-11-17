package hostagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

var ErrUnsupported = errors.New("unsupported")

// LogsBackend must open only an exact ID after freshly checking ownership and
// bounded-tail driver support. Close must interrupt a blocked Next call.
type LogsBackend interface {
	OpenLogs(context.Context, string, int, bool, bool) (LogReader, error)
}
type LogReader interface {
	Next() (stream string, chunk []byte, err error)
	Close() error
}
type logResult struct {
	stream string
	chunk  []byte
	err    error
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request, id string, body []byte) {
	var req struct {
		ContainerID string `json:"containerId"`
		Tail        int    `json:"tail"`
		Follow      bool   `json:"follow"`
		Timestamps  bool   `json:"timestamps"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if !exactObject(body, "containerId", "tail", "follow", "timestamps") || d.Decode(&req) != nil || !fullID.MatchString(req.ContainerID) || req.Tail < 1 || req.Tail > 1000 {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	// Null booleans are not booleans; decoding into a bool alone accepts null.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if string(fields["follow"]) != "true" && string(fields["follow"]) != "false" || string(fields["timestamps"]) != "true" && string(fields["timestamps"]) != "false" {
		fail(w, id, 400, "INVALID_REQUEST", "Invalid request.", false)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		fail(w, id, 413, "LIMIT_EXCEEDED", "Limit exceeded.", false)
		return
	}
	duration := 15 * time.Second
	if req.Follow {
		duration = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.Context(), duration)
	defer cancel()
	if code := s.logScope(ctx, req.ContainerID); code != "" {
		logPreError(w, id, code)
		return
	}
	reader, e := s.c.LogsBackend.OpenLogs(ctx, req.ContainerID, req.Tail, req.Follow, req.Timestamps)
	if errors.Is(e, ErrNotFound) {
		fail(w, id, 404, "NOT_FOUND", "Not found.", false)
		return
	}
	if errors.Is(e, ErrUnsupported) {
		fail(w, id, 501, "UNSUPPORTED", "Unsupported.", false)
		return
	}
	if e != nil {
		inventoryError(w, id, ctx)
		return
	}
	defer reader.Close()
	control := http.NewResponseController(w)
	// Cover net/http's final chunked-response flush after the handler returns.
	defer func() { _ = control.SetWriteDeadline(time.Now().Add(5 * time.Second)) }()
	if control.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		fail(w, id, 501, "UNSUPPORTED", "Unsupported.", false)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	if control.Flush() != nil || control.SetWriteDeadline(time.Time{}) != nil {
		return
	}
	write := func(data any) bool {
		b, e := json.Marshal(envelope{Version: Version, RequestID: id, Data: data})
		if e != nil {
			return false
		}
		b = append(b, '\n')
		if control.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
			return false
		}
		if _, e = w.Write(b); e != nil {
			return false
		}
		if control.Flush() != nil {
			return false
		}
		return control.SetWriteDeadline(time.Time{}) == nil
	}
	// A single bounded hand-off allows cancellation and periodic rechecks while
	// engine reads block. Closing the reader interrupts the outstanding read.
	results := make(chan logResult)
	go func() {
		for {
			stream, chunk, err := reader.Next()
			select {
			case results <- logResult{stream, chunk, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	recheck := time.NewTicker(10 * time.Second)
	defer recheck.Stop()
	seq := 0
	total := 0
	for {
		select {
		case <-ctx.Done():
			if r.Context().Err() == nil {
				write(map[string]any{"type": "end", "reason": "duration", "bytes": total})
			}
			return
		case <-heartbeat.C:
			seq++
			if !write(map[string]any{"type": "heartbeat", "sequence": seq}) {
				return
			}
		case <-recheck.C:
			if code := s.logScope(ctx, req.ContainerID); code != "" {
				write(map[string]any{"type": "error", "code": code, "message": logMessage(code)})
				return
			}
		case result := <-results:
			if result.err != nil {
				if errors.Is(result.err, io.EOF) {
					write(map[string]any{"type": "end", "reason": "complete", "bytes": total})
				} else {
					write(map[string]any{"type": "error", "code": "ENGINE_UNAVAILABLE", "message": "Engine unavailable."})
				}
				return
			}
			if len(result.chunk) == 0 || len(result.chunk) > 16384 || (result.stream != "stdout" && result.stream != "stderr" && result.stream != "unknown") {
				write(map[string]any{"type": "error", "code": "INTERNAL", "message": "Internal error."})
				return
			}
			remaining := (1 << 20) - total
			chunk := result.chunk
			if len(chunk) > remaining {
				chunk = chunk[:remaining]
			}
			if len(chunk) > 0 {
				seq++
				total += len(chunk)
				if !write(map[string]any{"type": "chunk", "sequence": seq, "stream": result.stream, "bytesBase64": base64.StdEncoding.EncodeToString(chunk)}) {
					return
				}
			}
			if total == 1<<20 {
				write(map[string]any{"type": "end", "reason": "byte_limit", "bytes": total})
				return
			}
		}
	}
}
func (s *Server) logScope(ctx context.Context, id string) string {
	daemon, e := s.c.Identity.DaemonID(ctx)
	if e != nil || daemon == "" {
		return "ENGINE_UNAVAILABLE"
	}
	if daemon != s.c.DaemonID {
		return "BINDING_CHANGED"
	}
	c, e := s.c.InventoryBackend.InventoryInspect(ctx, id)
	if errors.Is(e, ErrNotFound) {
		return "NOT_FOUND"
	}
	if e != nil {
		return "ENGINE_UNAVAILABLE"
	}
	if c.ID != id || c.Labels["dill.managed"] != "true" || c.Labels["dill.stack"] == "" || c.Labels["dill.service"] == "" {
		return "NOT_FOUND"
	}
	return ""
}
func logMessage(code string) string {
	switch code {
	case "NOT_FOUND":
		return "Not found."
	case "BINDING_CHANGED":
		return "Binding changed."
	default:
		return "Engine unavailable."
	}
}
func logPreError(w http.ResponseWriter, id, code string) {
	switch code {
	case "NOT_FOUND":
		fail(w, id, 404, code, logMessage(code), false)
	case "BINDING_CHANGED":
		fail(w, id, 409, code, logMessage(code), false)
	default:
		fail(w, id, 503, code, logMessage(code), true)
	}
}
