package orchestrator

import (
	"context"
	"errors"
	"fmt"
)

// RemovalTarget identifies requested containers, not arbitrary engine objects.
// Service may be empty for label-discovered cleanup; capture pins the live label.
type RemovalTarget struct{ Name, Service string }
type OwnedResource struct{ Name, Service, ID string }

// RemovalIntent is an in-process snapshot of owned container identities. It is
// not a persisted authorization token, rollback plan, or permission to delete
// volumes/images/networks. Capture it before requesting destructive confirmation.
type RemovalIntent struct {
	Stack     string
	Resources []OwnedResource
}

func CaptureRemovalIntent(ctx context.Context, engine Engine, stack string, targets []RemovalTarget) (*RemovalIntent, error) {
	intent := &RemovalIntent{Stack: stack}
	seen := map[string]bool{}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[target.Name] {
			continue
		}
		seen[target.Name] = true
		live, err := engine.InspectConfig(ctx, target.Name)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, &ResourceError{"inspect removal intent", target.Name, "", err}
		}
		service := target.Service
		if live != nil && service == "" {
			service = live.SystemLabels["dill.service"]
		}
		if err := CheckOwnership(target.Name, stack, service, live); err != nil {
			return nil, err
		}
		if live.ID == "" {
			return nil, &ResourceError{"capture removal intent", target.Name, "", errors.New("empty container ID")}
		}
		intent.Resources = append(intent.Resources, OwnedResource{target.Name, service, live.ID})
	}
	return intent, ctx.Err()
}

// Revalidate refuses same-name replacements, including replacements labelled as
// owned by the same stack. Subsequent mutations must use the captured ID only.
func (i *RemovalIntent) Revalidate(ctx context.Context, engine Engine, resource OwnedResource) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id, err := OwnedID(ctx, engine, resource.Name, i.Stack, resource.Service)
	if err != nil {
		return false, err
	}
	if id == "" {
		return false, nil
	}
	if id != resource.ID {
		return false, fmt.Errorf("%w: removal target %q changed since intent capture; retained", ErrOwnershipConflict, resource.Name)
	}
	return true, nil
}

// Remove revalidates before each mutation. Stop failures do not suppress an
// independent removal attempt; partial cleanup remains visible to the caller.
func (i *RemovalIntent) Remove(ctx context.Context, engine Engine, resource OwnedResource) error {
	ok, err := i.Revalidate(ctx, engine, resource)
	if err != nil || !ok {
		return err
	}
	var errs []error
	if err := engine.StopService(ctx, resource.ID); err != nil && !errors.Is(err, ErrNotFound) {
		errs = append(errs, &ResourceError{"stop", resource.Name, resource.ID, err})
	}
	ok, err = i.Revalidate(ctx, engine, resource)
	if err != nil {
		errs = append(errs, err)
	}
	if ok && err == nil {
		if err := engine.RemoveService(ctx, resource.ID); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, &ResourceError{"remove (resource may be retained)", resource.Name, resource.ID, err})
		}
	}
	return errors.Join(errs...)
}
