package config

import (
	"strings"
	"testing"
)

func TestParseBytes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {"1024", 1024}, {" 512M ", 512 << 20}, {"1.5g", 3 << 29},
		{"1KiB", 1024}, {"2tb", 2 << 40}, {".5k", 512}, {"+1g", 1 << 30},
		{"9223372036854775807", 1<<63 - 1}, {"9007199254740993", 9007199254740993},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseBytes(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"", "-1", "NaN", "Inf", "1/2", "0x10", "1e3", "wat", "1pb", ".1", "9223372036854775808", "9223372036854775807.1", "8388608t"} {
		t.Run("invalid/"+input, func(t *testing.T) {
			if n, err := ParseBytes(input); err == nil {
				t.Fatalf("accepted %q as %d", input, n)
			}
		})
	}
}

func TestNormalizeResources(t *testing.T) {
	shares, pids := 512, 100
	got, err := NormalizeResources(&Service{MemLimit: "512m", MemReservation: "128m", CPUs: "0.123456789", CPUShares: &shares, PidsLimit: &pids})
	want := ResourceLimits{Memory: 512 << 20, MemoryReservation: 128 << 20, NanoCPUs: 123450000, CPUShares: 512, PidsLimit: 100}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	empty, err := NormalizeResources(&Service{})
	if err != nil || !empty.IsZero() {
		t.Fatalf("unset resources: %+v, %v", empty, err)
	}
	unlimited := -1
	got, err = NormalizeResources(&Service{PidsLimit: &unlimited})
	if err != nil || got.PidsLimit != -1 || got.IsZero() {
		t.Fatalf("unlimited PIDs: %+v, %v", got, err)
	}
	for _, tc := range []struct {
		cpus string
		want int64
	}{{".5", 500000000}, {"1.000000001", 1000000000}, {"0.00001", 10000}, {"9223372036.854775807", 9223372036854770000}} {
		r, err := NormalizeResources(&Service{CPUs: tc.cpus})
		if err != nil || r.NanoCPUs != tc.want {
			t.Errorf("cpus %s: got %+v, %v", tc.cpus, r, err)
		}
	}
}

func TestValidateRuntimeResources(t *testing.T) {
	negativeShares, negativePids := -1, -2
	cases := []struct {
		name  string
		svc   Service
		field string
	}{
		{"memory", Service{MemLimit: "bad"}, "memLimit"},
		{"reservation", Service{MemReservation: "bad"}, "memReservation"},
		{"shares", Service{CPUShares: &negativeShares}, "cpuShares"},
		{"pids", Service{PidsLimit: &negativePids}, "pidsLimit"},
	}
	for _, cpu := range []string{"bad", "0", "-1", "NaN", "Inf", "0.0000000001", "0.000001", "10000000000", "9223372036.854775808"} {
		cases = append(cases, struct {
			name  string
			svc   Service
			field string
		}{"cpu/" + cpu, Service{CPUs: cpu}, "cpus"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &DillConfig{Services: map[string]*Service{"web": &tc.svc}}
			err := ValidateRuntime(cfg)
			if err == nil || !strings.Contains(err.Error(), `service "web"`) || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("expected service-scoped %s error, got %v", tc.field, err)
			}
		})
	}
}
