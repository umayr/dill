package preflight

import "testing"

func TestResourceLeavesKnownEmptyScopeNotMissingCapability(t *testing.T) {
	// Existing checked local-driver/ownership/no-options validation permits
	// absent scope strings. A known empty value is committed, not normalized
	// to "local". A missing Scope projection key remains an error (other tests).
	n := networkEvidence()
	editSettings(t, &n, func(m map[string]any) { m["Scope"] = "" })
	nDigest, err := NetworkDigest("synthetic", n)
	if err != nil {
		t.Fatal("known permitted empty scope inferred unknown", err)
	}
	base, err := NetworkDigest("synthetic", networkEvidence())
	if err != nil || nDigest == base {
		t.Fatal("scope spelling was not committed", err)
	}
	v := volumeEvidence()
	editSettings(t, &v, func(m map[string]any) { m["Scope"] = "" })
	vDigest, err := VolumeDigest("synthetic", v)
	if err != nil {
		t.Fatal(err)
	}
	base, err = VolumeDigest("synthetic", volumeEvidence())
	if err != nil || vDigest == base {
		t.Fatal("volume scope was not committed", err)
	}
}
