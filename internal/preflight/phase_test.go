package preflight

import "testing"

func phaseFailure(t *testing.T, code, outcome, coord string, actual bool, cleanup ...string) Result {
	t.Helper()
	r := sampleResult(t)
	r.Withdraw()
	r.Outcome = outcome
	r.CoordinationEffects = coord
	r.Errors = []Error{FixedError(code)}
	if !actual {
		r.Target = nil
	}
	if code == "SECRET_UNAVAILABLE" {
		r.Target = nil
		r.Stack = ""
	}
	for _, code := range cleanup {
		r.Errors = append(r.Errors, FixedError(code))
	}
	return r
}
func phaseCheck(t *testing.T, r Result, exit int, want bool) {
	t.Helper()
	err := r.Validate(exit)
	if (err == nil) != want {
		t.Fatalf("valid=%v want=%v result=%+v err=%v", err == nil, want, r, err)
	}
}
func TestFailurePhaseStaticPrestate(t *testing.T) {
	for _, code := range []string{"INVALID_ARGUMENTS", "UNSUPPORTED_OPERATION", "UNSUPPORTED_CONTRACT", "UNSUPPORTED_FORMAT", "INVALID_TARGET", "UNSUPPORTED_PREFLIGHT"} {
		t.Run(code, func(t *testing.T) {
			phaseCheck(t, phaseFailure(t, code, "invalid_input", "none", false), 2, true)
			phaseCheck(t, phaseFailure(t, code, "invalid_input", "none", true), 2, false)
			for _, coord := range []string{"written", "unknown"} {
				phaseCheck(t, phaseFailure(t, code, "invalid_input", coord, false), 2, false)
			}
			phaseCheck(t, phaseFailure(t, code, "unknown", "none", false, "ENGINE_CLOSE_FAILED"), 1, false)
		})
	}
	for _, code := range []string{"EVALUATION_FAILED", "QUALIFICATION_FAILED"} {
		t.Run(code, func(t *testing.T) {
			phaseCheck(t, phaseFailure(t, code, "failed", "none", false), 1, true)
			phaseCheck(t, phaseFailure(t, code, "unknown", "none", false, "ENGINE_CLOSE_FAILED"), 1, true)
			phaseCheck(t, phaseFailure(t, code, "failed", "none", true), 1, false)
			phaseCheck(t, phaseFailure(t, code, "failed", "written", false), 1, false)
			phaseCheck(t, phaseFailure(t, code, "unknown", "unknown", false, "COORDINATION_COMMIT_FAILED"), 1, false)
		})
	}
}
func TestFailurePhaseQualifiedBeforeStateIsNotStateProof(t *testing.T) {
	for _, actual := range []bool{false, true} {
		phaseCheck(t, phaseFailure(t, "CONFIG_INVALID", "invalid_input", "none", actual), 2, true)
		phaseCheck(t, phaseFailure(t, "CANCELLED", "cancelled", "none", actual), 1, true)
		phaseCheck(t, phaseFailure(t, "ENGINE_CLOSE_FAILED", "unknown", "none", actual), 1, true)
		phaseCheck(t, phaseFailure(t, "ENGINE_CLOSE_FAILED", "unknown", "unknown", actual), 1, true)
		phaseCheck(t, phaseFailure(t, "CONFIG_INVALID", "invalid_input", "written", actual), 2, false)
	}
	phaseCheck(t, phaseFailure(t, "CONFIG_INVALID", "failed", "written", true), 1, true)
}
func TestFailurePhaseCleanupAndSecret(t *testing.T) {
	for _, code := range []string{"COORDINATION_COMMIT_FAILED", "COORDINATION_RELEASE_FAILED"} {
		phaseCheck(t, phaseFailure(t, code, "unknown", "unknown", true), 1, true)
		for _, coord := range []string{"none", "written"} {
			phaseCheck(t, phaseFailure(t, code, "unknown", coord, true), 1, false)
		}
		phaseCheck(t, phaseFailure(t, code, "failed", "unknown", true), 1, false)
	}
	phaseCheck(t, phaseFailure(t, "ENGINE_CLOSE_FAILED", "unknown", "written", true), 1, false)
	for _, coord := range []string{"none", "written"} {
		phaseCheck(t, phaseFailure(t, "SECRET_UNAVAILABLE", "failed", coord, false), 1, true)
	}
	phaseCheck(t, phaseFailure(t, "SECRET_UNAVAILABLE", "failed", "unknown", false), 1, false)
	phaseCheck(t, phaseFailure(t, "SECRET_UNAVAILABLE", "unknown", "unknown", false, "COORDINATION_COMMIT_FAILED"), 1, true)
	leak := phaseFailure(t, "SECRET_UNAVAILABLE", "failed", "none", false)
	leak.Stack = "synthetic"
	phaseCheck(t, leak, 1, false)
	phaseCheck(t, phaseFailure(t, "PREFLIGHT_UNAVAILABLE", "unknown", "unknown", true, "ENGINE_CLOSE_FAILED", "COORDINATION_COMMIT_FAILED", "COORDINATION_RELEASE_FAILED"), 1, true)
	phaseCheck(t, phaseFailure(t, "PREFLIGHT_UNAVAILABLE", "unknown", "unknown", true, "COORDINATION_RELEASE_FAILED", "ENGINE_CLOSE_FAILED"), 1, false)
	phaseCheck(t, phaseFailure(t, "PREFLIGHT_UNAVAILABLE", "unknown", "unknown", true, "COORDINATION_COMMIT_FAILED", "COORDINATION_COMMIT_FAILED"), 1, false)
	phaseCheck(t, phaseFailure(t, "PREFLIGHT_UNAVAILABLE", "failed", "written", true, "COORDINATION_UNAVAILABLE"), 1, false)
}
