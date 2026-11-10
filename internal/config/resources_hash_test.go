package config

import "testing"

func TestServiceConfigHashResources(t *testing.T) {
	hash := func(svc *Service) string {
		t.Helper()
		h, err := ServiceConfigHash("stack", "app", svc)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	zero := 0
	base := hash(&Service{Image: "app:latest"})
	if got := hash(&Service{Image: "app:latest", MemLimit: "0", CPUShares: &zero, PidsLimit: &zero}); got != base {
		t.Fatal("explicit defaults should hash like omitted resources")
	}
	shares, pids := 512, 100
	for _, svc := range []*Service{
		{Image: "app:latest", Cpuset: "0-1"},
		{Image: "app:latest", MemLimit: "512m"},
		{Image: "app:latest", MemReservation: "128m"},
		{Image: "app:latest", CPUs: "0.5"},
		{Image: "app:latest", CPUShares: &shares},
		{Image: "app:latest", PidsLimit: &pids},
	} {
		if hash(svc) == base {
			t.Fatalf("resource change did not change hash: %+v", svc)
		}
	}
	a := &Service{Image: "app:latest", MemLimit: "512m", CPUs: "0.123456789"}
	b := &Service{Image: "app:latest", MemLimit: "536870912", CPUs: "0.12345"}
	if hash(a) != hash(b) {
		t.Fatal("equivalent memory sizes and CPU quotas should hash identically")
	}
}
