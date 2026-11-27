package preflight

import "testing"

func TestBrokerResultValidationDoesNotWidenOrdinary(t *testing.T) {
	r := sampleResult(t)
	r.Target.Engine = "podman"
	if r.Validate(0) == nil {
		t.Fatal("ordinary preflight admitted broker profile")
	}
	if err := r.ValidateBroker(0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Buffer(0); err == nil {
		t.Fatal("ordinary buffer widened broker target")
	}
	if _, err := r.BufferBroker(0); err != nil {
		t.Fatal("broker output cannot serialize validated target", err)
	}
	r.Withdraw()
	r.Outcome = "failed"
	r.Errors = []Error{FixedError("PREFLIGHT_UNAVAILABLE")}
	if _, err := r.BufferBroker(1); err != nil {
		t.Fatal("broker failure output unavailable", err)
	}
	r = sampleResult(t)
	r.Target.Engine = "podman"
	r.Target.QualifiedIdentityDigest = ""
	if r.ValidateBroker(0) == nil {
		t.Fatal("missing target evidence accepted")
	}
}
