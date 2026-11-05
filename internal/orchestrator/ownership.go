package orchestrator

import (
	"errors"
	"fmt"
)

// ErrOwnershipConflict means a container name is occupied by a container that
// does not belong to the expected Dill stack and service.
var ErrOwnershipConflict = errors.New("container ownership conflict")

// CheckOwnership rejects implicit adoption of existing containers by name.
func CheckOwnership(name, stack, service string, live *LiveConfig) error {
	if live != nil && live.SystemLabels["dill.managed"] == "true" &&
		live.SystemLabels["dill.stack"] == stack &&
		live.SystemLabels["dill.service"] == service && service != "" {
		return nil
	}
	return fmt.Errorf("%w: %q is not managed by stack %q, service %q; choose a different container name or explicitly remove the conflicting container", ErrOwnershipConflict, name, stack, service)
}
