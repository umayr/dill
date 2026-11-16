//go:build !linux && !darwin

package projectsnapshot

import (
	"context"
	"errors"
)

// Receive fails closed on platforms without a reviewed staging implementation.
func Receive(ctx context.Context, root string, s Snapshot, limits Limits) (string, error) {
	return "", errors.New("project snapshot staging is unsupported on this platform")
}
