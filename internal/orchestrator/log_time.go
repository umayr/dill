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
	now := time.Now()
	// Docker's parser treats bare "0" as Unix epoch, not a duration.
	if d, err := time.ParseDuration(s); s != "0" && err == nil {
		return now.Add(-d).UTC().Format(time.RFC3339Nano), nil
	}
	stamp, err := timetypes.GetTimestamp(s, now)
	if err != nil {
		return "", fmt.Errorf("invalid value for since %q: %w", s, err)
	}
	seconds, nanos, err := timetypes.ParseTimestamps(stamp, 0)
	if err != nil {
		return "", fmt.Errorf("invalid value for since %q: %w", s, err)
	}
	// Give both engines the same instant, including Docker's local-time forms.
	result := time.Unix(seconds, nanos).UTC().Format(time.RFC3339Nano)
	if _, err := time.Parse(time.RFC3339Nano, result); err != nil {
		return "", fmt.Errorf("since %q is outside RFC3339 range: %w", s, err)
	}
	return result, nil
}
