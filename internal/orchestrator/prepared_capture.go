package orchestrator

import (
	"strings"
	"sync"
)

// PreparedImage binds one compared service's ORIGINAL desired reference to the
// separately inspected immutable content. Never rewrite Service.Image to pin
// content: ownership/config-hash labels must retain desired-reference provenance.
type PreparedImage struct {
	Reference string
	ID        string
}

// PreparedImageEnforcer installs an invocation-local restriction, not engine
// authority or an image pull. It must be installed before checked execution and
// forwarded explicitly through every qualified engine decorator. Legacy engines
// without an installed restriction retain their existing behavior.
type PreparedImageEnforcer interface {
	EnforcePreparedImages(map[string]PreparedImage) error
}

type preparedImageConstraint struct {
	mu     sync.RWMutex
	active bool
	images map[string]PreparedImage
}

func checkedPreparedID(raw string) (string, bool) {
	id := strings.TrimPrefix(raw, "sha256:")
	if len(id) != 64 {
		return "", false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return "sha256:" + id, true
}
func (p *preparedImageConstraint) install(images map[string]PreparedImage) error {
	copy := make(map[string]PreparedImage, len(images))
	for service, image := range images {
		id, ok := checkedPreparedID(image.ID)
		if service == "" || image.Reference == "" || !ok {
			return ErrDisposableUnavailable
		}
		image.ID = id
		copy[service] = image
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active {
		return ErrDisposableUnavailable
	} // never replace a compared restriction
	p.images = copy
	p.active = true
	return nil
}
func (p *preparedImageConstraint) resolve(service, reference string) (string, bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active {
		return reference, false, nil
	}
	image, ok := p.images[service]
	if !ok || image.Reference != reference {
		return "", true, ErrDisposableUnavailable
	}
	return image.ID, true, nil
}
func preparedImageMatches(actual, expected string) bool {
	a, ok := checkedPreparedID(actual)
	return ok && a == expected
}
