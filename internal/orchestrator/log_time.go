package orchestrator

import "time"

// Convert relative durations once at the adapter boundary. Absolute values are
// passed through for the engine to validate, preserving existing CLI behavior.
func normalizeLogSince(s string) string {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return time.Now().Add(-d).UTC().Format(time.RFC3339Nano)
	}
	return s
}
