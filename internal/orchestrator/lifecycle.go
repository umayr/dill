package orchestrator

import (
	"context"
	"errors"
	"fmt"
)

// ResourceError identifies an unsuccessful lifecycle operation. Err remains
// available to errors.Is/As; this internal type is not a wire protocol.
type ResourceError struct {
	Operation, Resource, ID string
	Err                     error
}

func (e *ResourceError) Error() string {
	return fmt.Sprintf("%s %q (id %s): %v", e.Operation, e.Resource, e.ID, e.Err)
}
func (e *ResourceError) Unwrap() error { return e.Err }

// OwnedID binds later mutations to an immutable container identity.
func OwnedID(ctx context.Context, engine Engine, name, stack, service string) (string, error) {
	live, err := engine.InspectConfig(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", &ResourceError{"inspect", name, "", err}
	}
	if live == nil {
		return "", &ResourceError{"inspect", name, "", errors.New("empty configuration")}
	}
	if service == "" {
		service = live.SystemLabels["dill.service"]
	}
	if err := CheckOwnership(name, stack, service, live); err != nil {
		return "", err
	}
	if live.ID == "" {
		return "", &ResourceError{"inspect", name, "", errors.New("empty container ID")}
	}
	return live.ID, nil
}

// RemoveContainer attempts removal even if stopping failed, and reports both.
func RemoveContainer(ctx context.Context, engine Engine, name, id string) error {
	var errs []error
	if err := engine.StopService(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		errs = append(errs, &ResourceError{"stop", name, id, err})
	}
	if err := engine.RemoveService(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
		errs = append(errs, &ResourceError{"remove (resource may be retained)", name, id, err})
	}
	return errors.Join(errs...)
}
