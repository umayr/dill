package config

import "testing"

// These are normalization checks, not evidence of engine or cgroup enforcement.
func TestResourceBoundaryEquivalence(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b Service
		want ResourceLimits
	}{
		{"memory units", Service{MemLimit: "1g", MemReservation: "512m"}, Service{MemLimit: "1024m", MemReservation: "536870912"}, ResourceLimits{Memory: 1 << 30, MemoryReservation: 1 << 29}},
		{"cpu precision", Service{CPUs: "1.000009999"}, Service{CPUs: "1"}, ResourceLimits{NanoCPUs: 1000000000}},
		{"zero shares and pids", Service{CPUShares: intPointer(0), PidsLimit: intPointer(0)}, Service{}, ResourceLimits{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := NormalizeResources(&tc.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := NormalizeResources(&tc.b)
			if err != nil {
				t.Fatal(err)
			}
			if a != tc.want || b != tc.want {
				t.Fatalf("normalized resources: %+v and %+v; want %+v", a, b, tc.want)
			}
		})
	}
}

func intPointer(v int) *int { return &v }
