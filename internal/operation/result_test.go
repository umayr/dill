package operation

import (
	"errors"
	"sync"
	"testing"
)

func TestRecorderEffectsAreConservative(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, effect string
		calls                 int
		fail, pending         bool
	}{
		{"no calls", "failed", "none", 0, false, false},
		{"success", "succeeded", "complete", 1, false, false},
		{"later failure", "failed", "partial", 1, false, false},
		{"ambiguous engine error", "failed", "unknown", 1, true, false},
		{"inflight snapshot", "cancelled", "unknown", 1, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r Recorder
			for i := 0; i < tc.calls; i++ {
				done := r.Begin("container", "PRIVATE_ID", "stop")
				if !tc.pending {
					var err error
					if tc.fail {
						err = errors.New("PRIVATE_ERROR")
					}
					done("", err)
				}
			}
			result := Result{Outcome: tc.outcome}
			r.Finish(&result)
			if result.Effects != tc.effect {
				t.Fatalf("%+v", result)
			}
			for _, resource := range result.Resources {
				if resource.IdentityDigest != ResourceDigest("container", "PRIVATE_ID") {
					t.Fatalf("identity not domain-separated: %+v", resource)
				}
			}
		})
	}
}

func TestRecorderConcurrentAndBounded(t *testing.T) {
	var r Recorder
	var wg sync.WaitGroup
	for i := 0; i < 4200; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); done := r.Begin("container", "id", "start"); done("", nil) }()
	}
	wg.Wait()
	result := Result{Outcome: "succeeded"}
	r.Finish(&result)
	if result.Effects != "complete" || !result.ResourcesTruncated || len(result.Resources) != 4096 {
		t.Fatalf("effects=%s truncated=%v count=%d", result.Effects, result.ResourcesTruncated, len(result.Resources))
	}
	// Truncation must not erase an uncertain call from aggregate effects.
	r.Begin("container", "id", "stop")
	r.Finish(&result)
	if result.Effects != "unknown" {
		t.Fatal(result.Effects)
	}
	if ResourceDigest("container", "id") == ResourceDigest("image", "id") {
		t.Fatal("cross-kind identity collision")
	}
}
