package preflight

import "testing"

func TestFailureEmptyOperationOnlyStaticOrGlobalSecretGuard(t *testing.T) {
	for _, code := range []string{"INVALID_ARGUMENTS", "UNSUPPORTED_OPERATION", "UNSUPPORTED_CONTRACT", "UNSUPPORTED_FORMAT", "INVALID_TARGET", "UNSUPPORTED_PREFLIGHT"} {
		r := phaseFailure(t, code, "invalid_input", "none", false)
		r.Operation = ""
		phaseCheck(t, r, 2, true)
	}
	r := phaseFailure(t, "SECRET_UNAVAILABLE", "failed", "none", false)
	r.Operation = ""
	phaseCheck(t, r, 1, true)
	r.CoordinationEffects = "written"
	phaseCheck(t, r, 1, false)
	r = phaseFailure(t, "SECRET_UNAVAILABLE", "unknown", "unknown", false, "ENGINE_CLOSE_FAILED")
	r.Operation = ""
	phaseCheck(t, r, 1, false)
	for _, tc := range []struct{ code, outcome, coord string }{
		{"EVALUATION_FAILED", "failed", "none"},
		{"QUALIFICATION_FAILED", "failed", "none"},
		{"CONFIG_INVALID", "invalid_input", "none"},
		{"COORDINATION_UNAVAILABLE", "unknown", "unknown"},
		{"PREFLIGHT_UNAVAILABLE", "failed", "written"},
		{"PREFLIGHT_LIMIT_EXCEEDED", "failed", "none"},
		{"CANCELLED", "cancelled", "none"},
		{"ENGINE_CLOSE_FAILED", "unknown", "none"},
		{"COORDINATION_COMMIT_FAILED", "unknown", "unknown"},
		{"COORDINATION_RELEASE_FAILED", "unknown", "unknown"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := phaseFailure(t, tc.code, tc.outcome, tc.coord, false)
			r.Operation = ""
			if tc.code == "PREFLIGHT_LIMIT_EXCEEDED" {
				r.ResourcesTruncated = true
			}
			exit := 1
			if tc.outcome == "invalid_input" {
				exit = 2
			}
			phaseCheck(t, r, exit, false)
		})
	}
}
