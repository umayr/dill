package preflight

import "testing"

// Literal synthetic PRIVATE-leaf oracles, independently calculated with
// Python hashlib + manually authored, sorted compact JSON. Not derived using
// this Go serializer and NOT part of the frozen public wire/canonical corpus.
func TestPrivateLeavesIndependentSyntheticOracle(t *testing.T) {
	live, err := ContainerDigest("synthetic", "web", "synthetic_web", syntheticLive())
	if err != nil {
		t.Fatal(err)
	}
	network, err := NetworkDigest("synthetic", networkEvidence())
	if err != nil {
		t.Fatal(err)
	}
	volume, err := VolumeDigest("synthetic", volumeEvidence())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, got, want string }{
		{"live", live, "sha256:ccb8ff9afb11fdbfd03d16074229f3fb57d867373c826b2b3f43ad4502aefcc8"},
		{"network", network, "sha256:333ea7becb5900c94354f6acef02ef39d0b2bf5e332d2b6fde5ff1d80353c791"},
		{"volume", volume, "sha256:01dfaab4d1adc14c29c99bc5bb05c782d605b2394582d60f425437e6a3751def"},
	} {
		if test.got != test.want {
			t.Errorf("%s private leaf changed: got %s, want %s", test.name, test.got, test.want)
		}
	}
}
