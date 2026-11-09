package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const failedStartCleanupTimeout = 60 * time.Second

// startCreated removes only the ID returned by a successful create if starting
// fails. Cleanup uses an independent deadline even if deployment is canceled.
func startCreated(ctx, cleanupBase context.Context, name, id string, start func() error, remove func(context.Context, string) error) error {
	if id == "" {
		return fmt.Errorf("create %s returned an empty ID; outcome unknown and container may be retained", name)
	}
	err := ctx.Err()
	if err == nil {
		err = start()
	}
	if err == nil {
		return nil
	}
	if isPortConflict(err) {
		err = fmt.Errorf("port conflict: another process is bound to one of the requested host ports (%w)", err)
	} else {
		err = fmt.Errorf("start %s: %w", name, err)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupBase), failedStartCleanupTimeout)
	defer cancel()
	if cleanupErr := remove(cleanupCtx, id); cleanupErr != nil {
		return errors.Join(err, fmt.Errorf("remove failed-start container %s: %w", id, cleanupErr))
	}
	return err
}
