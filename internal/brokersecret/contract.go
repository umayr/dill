// Package brokersecret defines the new provider-neutral Brine-to-Dill static
// secret contract. It is independent of retired Alpha secret entrypoints.
package brokersecret

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const Version = "dill.broker-secrets/1"
const (
	MaxValue   = 64 << 10
	MaxBundle  = 1 << 20
	MaxWire    = 2 << 20
	MaxEntries = 128
)

var (
	ErrInvalid     = errors.New("invalid broker secret delivery")
	ErrUnavailable = errors.New("broker secret delivery unavailable")
	ErrUnsupported = errors.New("broker secret delivery unsupported")
	ErrExpired     = errors.New("broker secret review expired")
	ErrCapacity    = errors.New("broker secret capacity reached")
)

type Scope struct {
	Registration    string `json:"registration"`
	Stack           string `json:"stack"`
	Engine          string `json:"engine"`
	TargetDigest    string `json:"targetDigest"`
	SourceDigest    string `json:"sourceDigest"`
	ConfigDigest    string `json:"configDigest"`
	EndpointDigest  string `json:"endpointDigest"`
	BindingRevision uint64 `json:"bindingRevision"`
	PolicyRevision  string `json:"policyRevision"`
	Generation      string `json:"generation"`
}
type Grant struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	UID     int64  `json:"uid"`
	GID     int64  `json:"gid"`
	Mode    uint32 `json:"mode"`
}
type Item struct {
	Name string
	data func() []byte
}

func NewItem(name string, data []byte) (Item, error) {
	if !nameOK(name) || len(data) > MaxValue {
		return Item{}, ErrInvalid
	}
	payload := append([]byte{}, data...)
	return Item{Name: name, data: func() []byte { return payload }}, nil
}
func (i Item) bytes() []byte {
	if i.data == nil {
		return nil
	}
	return i.data()
}
func (i Item) Bytes() []byte              { return append([]byte(nil), i.bytes()...) }
func (Item) Format(w fmt.State, _ rune)   { _, _ = io.WriteString(w, "SecretItem{redacted}") }
func (Item) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

type Envelope struct {
	Scope Scope
	Items []Item
}

func (Envelope) Format(w fmt.State, _ rune)   { _, _ = io.WriteString(w, "SecretEnvelope{redacted}") }
func (Envelope) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }
func (e *Envelope) Close() {
	if e != nil {
		for _, i := range e.Items {
			clear(i.bytes())
		}
		e.Items = nil
	}
}

// ValidHandle validates only syntax, never authority or existence.
func ValidHandle(s string) bool { return validID(s) }

func validID(s string) bool {
	if len(s) != 32 || strings.ToLower(s) != s {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16
}
func validDigest(s string) bool {
	return len(s) == 71 && strings.HasPrefix(s, "sha256:") && strings.ToLower(s) == s && func() bool { b, e := hex.DecodeString(s[7:]); return e == nil && len(b) == 32 }()
}
func nameOK(s string) bool {
	if len(s) < 1 || len(s) > 63 {
		return false
	}
	for n, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || n > 0 && (c == '-' || c == '_' || c == '.')) {
			return false
		}
	}
	return s != "." && s != ".."
}
func (s Scope) Valid() bool {
	return validID(s.Registration) && nameOK(s.Stack) && (s.Engine == "docker" || s.Engine == "podman") && validDigest(s.TargetDigest) && validDigest(s.SourceDigest) && validDigest(s.ConfigDigest) && validDigest(s.EndpointDigest) && validDigest(s.PolicyRevision) && validID(s.Generation)
}
func (e Envelope) Validate(expected Scope, grants []Grant) error {
	if !expected.Valid() || e.Scope != expected || len(e.Items) == 0 || len(e.Items) > MaxEntries || len(grants) == 0 || len(grants) > MaxEntries {
		return ErrInvalid
	}
	names := map[string]bool{}
	size := 0
	for _, i := range e.Items {
		if !nameOK(i.Name) || i.data == nil || names[i.Name] || len(i.bytes()) > MaxValue {
			return ErrInvalid
		}
		names[i.Name] = true
		size += len(i.bytes())
	}
	if size > MaxBundle {
		return ErrCapacity
	}
	return validateGrants(names, grants)
}
func validateGrants(names map[string]bool, grants []Grant) error {
	if len(grants) == 0 || len(grants) > MaxEntries {
		return ErrInvalid
	}
	used := map[string]bool{}
	targets := map[string]bool{}
	pairs := map[string]bool{}
	for _, g := range grants {
		key := g.Service + "/" + g.Target
		pair := g.Service + "/" + g.Name
		if !nameOK(g.Service) || !names[g.Name] || !strings.HasPrefix(g.Target, "/run/secrets/") || path.Clean(g.Target) != g.Target || strings.ContainsAny(g.Target, "\x00\\\r\n") || len(g.Target) > 512 || g.UID < 0 || g.UID > 2147483647 || g.GID < 0 || g.GID > 2147483647 || (g.Mode != 0400 && g.Mode != 0440) || targets[key] || pairs[pair] {
			return ErrInvalid
		}
		targets[key] = true
		pairs[pair] = true
		used[g.Name] = true
	}
	if len(used) != len(names) {
		return ErrInvalid
	}
	return nil
}

type wireItem struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}
type wireEnvelope struct {
	Contract string     `json:"contract"`
	Scope    Scope      `json:"scope"`
	Items    []wireItem `json:"items"`
}

// Encode is an explicit private transport boundary. Never log or retain its
// output in operation metadata, source captures, arguments or diagnostics.
func Encode(e Envelope) ([]byte, error) {
	if !e.Scope.Valid() || len(e.Items) == 0 || len(e.Items) > MaxEntries {
		return nil, ErrInvalid
	}
	w := wireEnvelope{Contract: Version, Scope: e.Scope}
	size := 0
	seen := map[string]bool{}
	for _, i := range e.Items {
		if !nameOK(i.Name) || i.data == nil || seen[i.Name] || len(i.bytes()) > MaxValue {
			return nil, ErrInvalid
		}
		seen[i.Name] = true
		size += len(i.bytes())
		w.Items = append(w.Items, wireItem{i.Name, i.bytes()})
	}
	if size > MaxBundle {
		return nil, ErrCapacity
	}
	out, err := json.Marshal(w)
	if err != nil || len(out) > MaxWire {
		return nil, ErrInvalid
	}
	return out, nil
}
func Decode(r io.Reader) (Envelope, error) {
	var out Envelope
	if r == nil {
		return out, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxWire+1))
	defer clear(raw)
	if err != nil || len(raw) > MaxWire || !uniqueJSON(raw) {
		return out, ErrInvalid
	}
	var w wireEnvelope
	defer func() {
		for _, item := range w.Items {
			clear(item.Data)
		}
	}()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&w) != nil || w.Contract != Version || !w.Scope.Valid() || len(w.Items) == 0 || len(w.Items) > MaxEntries {
		return out, ErrInvalid
	}
	out.Scope = w.Scope
	seen := map[string]bool{}
	size := 0
	for _, i := range w.Items {
		if !nameOK(i.Name) || i.Data == nil || seen[i.Name] || len(i.Data) > MaxValue {
			out.Close()
			return Envelope{}, ErrInvalid
		}
		seen[i.Name] = true
		size += len(i.Data)
		item, err := NewItem(i.Name, i.Data)
		if err != nil {
			out.Close()
			return Envelope{}, ErrInvalid
		}
		out.Items = append(out.Items, item)
		clear(i.Data)
	}
	if size > MaxBundle {
		out.Close()
		return Envelope{}, ErrCapacity
	}
	return out, nil
}

// Reject duplicate object keys at every depth before decoding into structs.
func uniqueJSON(raw []byte) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 16 {
			return false
		}
		tok, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				key, ok := k.(string)
				if err != nil || !ok || seen[key] {
					return false
				}
				seen[key] = true
				if !value(depth + 1) {
					return false
				}
			}
			last, err := d.Token()
			return err == nil && last == json.Delim('}')
		case '[':
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			last, err := d.Token()
			return err == nil && last == json.Delim(']')
		default:
			return false
		}
	}
	if !value(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
