package preflight

import "errors"

// validateFailurePhase enforces only the shared revision4 A-F clarification.
// Actual target presence does not prove state access: qualification precedes it.
func (r Result) validateFailurePhase(exit int) error {
	bad := errors.New("incoherent preflight failure stage")
	cleanup := false
	last := 0
	primary := ""
	seen := map[string]bool{}
	hasClose, hasCommit, hasRelease := false, false, false
	for i, e := range r.Errors {
		if seen[e.Code] {
			return bad
		}
		seen[e.Code] = true
		order := 0
		switch e.Code {
		case "ENGINE_CLOSE_FAILED":
			order = 1
			hasClose = true
		case "COORDINATION_COMMIT_FAILED":
			order = 2
			hasCommit = true
		case "COORDINATION_RELEASE_FAILED":
			order = 3
			hasRelease = true
		}
		if order == 0 {
			if i != 0 {
				return bad
			}
			primary = e.Code
		} else {
			if order <= last {
				return bad
			}
			last = order
			cleanup = true
		}
	}
	if r.Operation == "" {
		if cleanup {
			return bad
		}
		switch primary {
		case "INVALID_ARGUMENTS", "UNSUPPORTED_OPERATION", "UNSUPPORTED_CONTRACT", "UNSUPPORTED_FORMAT", "INVALID_TARGET", "UNSUPPORTED_PREFLIGHT":
		case "SECRET_UNAVAILABLE":
			if r.CoordinationEffects != "none" {
				return bad
			}
		default:
			return bad
		}
	}
	if cleanup && (exit != 1 || r.Outcome != "unknown") {
		return bad
	}
	if hasCommit || hasRelease {
		if r.CoordinationEffects != "unknown" {
			return bad
		}
	}
	if hasClose && r.CoordinationEffects == "written" {
		return bad
	}
	switch primary {
	case "INVALID_ARGUMENTS", "UNSUPPORTED_OPERATION", "UNSUPPORTED_CONTRACT", "UNSUPPORTED_FORMAT", "INVALID_TARGET", "UNSUPPORTED_PREFLIGHT":
		if cleanup || r.Target != nil || r.CoordinationEffects != "none" || r.Outcome != "invalid_input" || exit != 2 {
			return bad
		}
	case "EVALUATION_FAILED", "QUALIFICATION_FAILED":
		if r.Target != nil || r.CoordinationEffects != "none" || hasCommit || hasRelease {
			return bad
		}
		if !cleanup && (r.Outcome != "failed" || exit != 1) {
			return bad
		}
	case "CONFIG_INVALID":
		if r.Outcome == "invalid_input" && r.CoordinationEffects != "none" {
			return bad
		}
		// An actually qualified target before Begin is legitimate. Do not infer
		// an engine/metadata phase from its presence or require it to be null.
	case "SECRET_UNAVAILABLE":
		if !cleanup && (r.Outcome != "failed" || exit != 1 || r.CoordinationEffects == "unknown") {
			return bad
		}
	}
	return nil
}
