package orchestrator

import (
	"fmt"
	"time"

	timetypes "github.com/docker/docker/api/types/time"
)

// Convert relative durations once and validate absolute values consistently.
func normalizeLogSince(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d).UTC().Format(time.RFC3339Nano), nil
	}
	if _, err := timetypes.GetTimestamp(s, time.Now()); err != nil {
		return "", fmt.Errorf("invalid value for since %q: %w", s, err)
	}
	return s, nil
}
