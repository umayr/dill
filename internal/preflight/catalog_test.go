package preflight

import "testing"

// Independent literal oracle transcribed from the shared revision4 contract,
// not populated by iterating the producer map itself. No raw diagnostics.
func TestFrozenLiteralErrorCatalog(t *testing.T) {
	want := map[string]string{
		"INVALID_ARGUMENTS":           "Invalid preflight arguments.",
		"UNSUPPORTED_OPERATION":       "Unsupported preflight operation.",
		"UNSUPPORTED_CONTRACT":        "Unsupported preflight contract.",
		"UNSUPPORTED_FORMAT":          "Unsupported preflight format.",
		"INVALID_TARGET":              "Explicit local Docker target and stack are required.",
		"UNSUPPORTED_PREFLIGHT":       "Unsupported qualified preflight invocation.",
		"SECRET_UNAVAILABLE":          "Secret configuration or source unavailable.",
		"EVALUATION_FAILED":           "Configuration evaluation failed.",
		"CONFIG_INVALID":              "Configuration is invalid.",
		"QUALIFICATION_FAILED":        "Qualified target unavailable.",
		"COORDINATION_UNAVAILABLE":    "Trusted coordination state unavailable. Inspect before retrying.",
		"PREFLIGHT_UNAVAILABLE":       "Complete preflight evidence unavailable.",
		"PREFLIGHT_LIMIT_EXCEEDED":    "Preflight response exceeds supported limits.",
		"CANCELLED":                   "Preflight cancelled or deadline exceeded.",
		"ENGINE_CLOSE_FAILED":         "Preflight transport cleanup failed.",
		"COORDINATION_COMMIT_FAILED":  "Durable preflight outcome unavailable. Inspect before retrying.",
		"COORDINATION_RELEASE_FAILED": "Preflight coordination cleanup failed. Inspect before retrying.",
	}
	if len(messages) != len(want) {
		t.Fatal("fixed catalog changed")
	}
	for code, message := range want {
		got := FixedError(code)
		if got.Code != code || got.Message != message {
			t.Fatalf("fixed literal mismatch for %s: %+v", code, got)
		}
	}
	if got := FixedError("SYNTHETIC_UNTRUSTED_STRING"); got.Code != "PREFLIGHT_UNAVAILABLE" || got.Message != "Complete preflight evidence unavailable." {
		t.Fatal("unknown diagnostic reflected")
	}
}
