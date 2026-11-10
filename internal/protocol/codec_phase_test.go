package protocol

import (
	"encoding/json"
	"testing"
)

// Exercise encoder, single-record decoder and stream causality independently:
// marshaling raw test values must not be gated by the encoder under test.
func assertPhaseRecord(t *testing.T, r Record, want bool) {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if _, err := Decode(raw); (err == nil) != want {
		t.Fatalf("Decode valid=%v want=%v: %s", err == nil, want, raw)
	}
	encoded, err := Encode(r)
	if (err == nil) != want || (!want && encoded != nil) {
		t.Fatalf("Encode valid=%v want=%v", err == nil, want)
	}
	stream := raw
	if _, ok := r.(Result); ok {
		prefix, err := Encode(accepted(fixtureID))
		if err != nil {
			t.Fatal(err)
		}
		stream = append(prefix, raw...)
	}
	if _, err := DecodeStream(stream); (err == nil) != want {
		t.Fatalf("DecodeStream valid=%v want=%v: %s", err == nil, want, stream)
	}
}

func TestCodecErrorPhaseMatrix(t *testing.T) {
	// Independent expected phase table, not production predicates. Every fixed
	// code is checked against every outcome and accepted cleanup axis.
	cases := []struct {
		code                Code
		bootstrap, accepted string
	}{
		{InvalidArguments, "rejected", ""},
		{InvalidRequest, "rejected", ""},
		{RequestTooLarge, "rejected", ""},
		{UnsupportedProtocol, "rejected", ""},
		{UnsupportedOperation, "rejected", ""},
		{UnsupportedFeature, "rejected", ""},
		{UnsupportedTransport, "failed", ""},
		{InternalError, "failed", "failed"},
		{Cancelled, "cancelled", "cancelled"},
		{DeadlineExceeded, "cancelled", "cancelled"},
		{OutputFailure, "", ""},
	}
	if len(cases) != len(messages) {
		t.Fatal("fixed error missing from exhaustive phase table")
	}
	seen := map[Code]bool{}
	for _, tt := range cases {
		if _, ok := messages[tt.code]; !ok || seen[tt.code] {
			t.Fatal("phase table code", tt.code)
		}
		seen[tt.code] = true
		for _, outcome := range []string{"succeeded", "rejected", "failed", "cancelled", "unknown"} {
			t.Run(string(tt.code)+"/"+outcome, func(t *testing.T) {
				b := failure(tt.code)
				b.Outcome = outcome
				assertPhaseRecord(t, b, tt.bootstrap != "" && outcome == tt.bootstrap)
				for _, cleanup := range []string{"notNeeded", "succeeded", "failed", "unknown"} {
					r := final(fixtureID, tt.code)
					r.Outcome = outcome
					r.Cleanup = cleanup
					want := tt.accepted != "" && outcome == tt.accepted
					// Unknown completion is a synthetic result axis, not a
					// compatibility rejection or cancellation assertion.
					if tt.code == InternalError && outcome == "unknown" {
						want = true
					}
					assertPhaseRecord(t, r, want)
				}
			})
		}
		// A valid INTERNAL_ERROR cannot launder a forbidden phase code.
		if tt.accepted == "" {
			t.Run(string(tt.code)+"/mixed errors", func(t *testing.T) {
				r := final(fixtureID, InternalError)
				r.Errors = append(r.Errors, WireError{tt.code, tt.code.Message()})
				assertPhaseRecord(t, r, false)
			})
		}
	}
}

func TestCodecCancellationAndIndependentCleanup(t *testing.T) {
	for _, cancelCode := range []Code{Cancelled, DeadlineExceeded} {
		for _, outcome := range []string{"succeeded", "rejected", "failed", "cancelled", "unknown"} {
			for _, cleanup := range []string{"notNeeded", "succeeded", "failed", "unknown"} {
				t.Run(string(cancelCode)+"/"+outcome+"/"+cleanup, func(t *testing.T) {
					r := final(fixtureID, cancelCode)
					r.Outcome = outcome
					r.Cleanup = cleanup
					r.Effects = "partial"
					r.Errors = append(r.Errors, WireError{InternalError, InternalError.Message()})
					// Preserve independent cleanup failure after cancellation,
					// without disguising a failed/unknown operation as cancelled.
					assertPhaseRecord(t, r, outcome == "cancelled" && cleanup == "failed")
				})
			}
		}
	}
	for _, outcome := range []string{"succeeded", "failed", "cancelled", "unknown"} {
		r := final(fixtureID, "")
		r.Outcome = outcome
		assertPhaseRecord(t, r, outcome != "cancelled") // cancelled requires a cancellation/deadline code
	}
	unknown := final(fixtureID, InternalError)
	unknown.Outcome = "unknown"
	unknown.Effects = "unknown"
	unknown.Cleanup = "unknown"
	assertPhaseRecord(t, unknown, true)
	// Both approved failure+cleanup-failure and unknown-without-errors fixtures
	// continue to be checked verbatim by TestSharedFixtures.
}
