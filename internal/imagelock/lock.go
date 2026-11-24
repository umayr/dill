// Package imagelock defines the version 1 image lock file. It does not mutate config.
package imagelock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"

	"github.com/distribution/reference"
)

// Lock maps service and job names to their original and resolved images.
type Lock struct {
	Version  int              `json:"version"`
	Services map[string]Entry `json:"services"`
	Jobs     map[string]Entry `json:"jobs"`
}
type Entry struct {
	Image  string `json:"image"`
	Digest string `json:"digest"`
}

var digestPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+@sha256:[a-f0-9]{64}$`)

// Parse rejects unknown fields, duplicate keys, trailing data and invalid entries.
func Parse(data []byte) (*Lock, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := value(d); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var l Lock
	if err := d.Decode(&l); err != nil {
		return nil, err
	}
	if err := l.validate(); err != nil {
		return nil, err
	}
	return &l, nil
}
func value(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate or invalid key %v", k)
			}
			seen[s] = true
			if err := value(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('['):
		for d.More() {
			if err := value(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	}
	return nil
}
func (l *Lock) validate() error {
	if l == nil || l.Version != 1 || l.Services == nil || l.Jobs == nil {
		return fmt.Errorf("invalid image lock version or sections")
	}
	for _, section := range []map[string]Entry{l.Services, l.Jobs} {
		for key, e := range section {
			if key == "" || e.Image == "" {
				return fmt.Errorf("empty key or image")
			}
			original, err := reference.ParseNormalizedNamed(e.Image)
			if err != nil {
				return fmt.Errorf("invalid image for %q: %w", key, err)
			}
			if !digestPattern.MatchString(e.Digest) {
				return fmt.Errorf("invalid registry digest for %q", key)
			}
			repo := e.Digest[:len(e.Digest)-72]
			if repo != original.Name() {
				return fmt.Errorf("repository mismatch for %q", key)
			}
			if pinned, ok := original.(reference.Digested); ok && e.Digest != repo+"@"+pinned.Digest().String() {
				return fmt.Errorf("pinned digest mismatch for %q", key)
			}
			if _, err := reference.ParseNamed(repo); err != nil {
				return fmt.Errorf("invalid repository for %q: %w", key, err)
			}
		}
	}
	return nil
}

// Resolve checks exact coverage and original images before returning pinned images.
// The input maps contain all service and job keys and their original images.
func (l *Lock) Resolve(services, jobs map[string]string) (map[string]string, map[string]string, error) {
	if err := l.validate(); err != nil {
		return nil, nil, err
	}
	resolve := func(entries map[string]Entry, expected map[string]string) (map[string]string, error) {
		if len(entries) != len(expected) {
			return nil, fmt.Errorf("missing or stale lock entries")
		}
		out := make(map[string]string, len(expected))
		for k, image := range expected {
			e, ok := entries[k]
			if !ok || e.Image != image {
				return nil, fmt.Errorf("missing or stale lock entry %q", k)
			}
			out[k] = e.Digest
		}
		return out, nil
	}
	s, err := resolve(l.Services, services)
	if err != nil {
		return nil, nil, err
	}
	j, err := resolve(l.Jobs, jobs)
	if err != nil {
		return nil, nil, err
	}
	return s, j, nil
}
