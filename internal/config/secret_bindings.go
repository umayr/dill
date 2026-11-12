package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode/utf8"
)

// ErrSecretUnavailable deliberately does not disclose input or policy details.
var ErrSecretUnavailable = errors.New("Secret configuration or source unavailable.")

var secretIdentifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// SecretBinding contains only non-sensitive operator-selected references.
type SecretBinding struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Revision string `json:"revision"`
}

// SecretBindings is an operator-selected deployment binding document.
type SecretBindings struct {
	Version  string          `json:"version"`
	Target   string          `json:"target"`
	Stack    string          `json:"stack"`
	Bindings []SecretBinding `json:"bindings"`
}

// ParseSecretBindings parses a bounded, strictly shaped binding document.
// It never embeds the document or parser diagnostics in the returned error.
func ParseSecretBindings(r io.Reader) (*SecretBindings, error) {
	data, err := io.ReadAll(io.LimitReader(r, 64*1024+1))
	if err != nil || len(data) > 64*1024 || !utf8.Valid(data) {
		return nil, ErrSecretUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(data))
	var result SecretBindings
	err = secretObject(d, map[string]func() error{
		"version": func() error { return secretString(d, &result.Version) },
		"target":  func() error { return secretString(d, &result.Target) },
		"stack":   func() error { return secretString(d, &result.Stack) },
		"bindings": func() error {
			if err := secretDelim(d, '['); err != nil {
				return err
			}
			result.Bindings = make([]SecretBinding, 0)
			for d.More() {
				if len(result.Bindings) >= 256 {
					return ErrSecretUnavailable
				}
				var b SecretBinding
				if err := secretObject(d, map[string]func() error{
					"name":     func() error { return secretString(d, &b.Name) },
					"source":   func() error { return secretString(d, &b.Source) },
					"revision": func() error { return secretString(d, &b.Revision) },
				}); err != nil {
					return err
				}
				result.Bindings = append(result.Bindings, b)
			}
			return secretDelim(d, ']')
		},
	})
	if err != nil {
		return nil, ErrSecretUnavailable
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrSecretUnavailable
	}
	if result.Version != "dill.secret-bindings/1" || !secretIdentifier.MatchString(result.Target) || !secretIdentifier.MatchString(result.Stack) {
		return nil, ErrSecretUnavailable
	}
	seen := make(map[string]bool, len(result.Bindings))
	for _, b := range result.Bindings {
		if !secretIdentifier.MatchString(b.Name) || !secretIdentifier.MatchString(b.Source) || !secretIdentifier.MatchString(b.Revision) || seen[b.Name] {
			return nil, ErrSecretUnavailable
		}
		seen[b.Name] = true
	}
	return &result, nil
}

func secretDelim(d *json.Decoder, want json.Delim) error {
	t, err := d.Token()
	if err != nil || t != want {
		return ErrSecretUnavailable
	}
	return nil
}

func secretString(d *json.Decoder, out *string) error {
	t, err := d.Token()
	if err != nil {
		return ErrSecretUnavailable
	}
	s, ok := t.(string)
	if !ok || len(s) > 128 {
		return ErrSecretUnavailable
	}
	*out = s
	return nil
}

func secretObject(d *json.Decoder, fields map[string]func() error) error {
	if err := secretDelim(d, '{'); err != nil {
		return err
	}
	seen := make(map[string]bool, len(fields))
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return ErrSecretUnavailable
		}
		key, ok := t.(string)
		if !ok || len(key) > 128 || seen[key] {
			return ErrSecretUnavailable
		}
		read, ok := fields[key]
		if !ok {
			return ErrSecretUnavailable
		}
		seen[key] = true
		if err := read(); err != nil {
			return err
		}
	}
	if len(seen) != len(fields) {
		return ErrSecretUnavailable
	}
	return secretDelim(d, '}')
}
