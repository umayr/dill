package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
)

func TestCapabilityRejections(t *testing.T) {
	cases := []struct {
		name     string
		svc      *config.Service
		rejected bool
	}{
		{"devices", &config.Service{Devices: []string{"/dev/a:/dev/b:r"}}, true},
		{"namespace", &config.Service{NetworkMode: "container:other"}, true},
		{"host", &config.Service{NetworkMode: "host"}, false},
		{"none", &config.Service{NetworkMode: "none"}, false},
		{"unpublished", &config.Service{Ports: []json.RawMessage{json.RawMessage(`"80"`)}}, true},
		{"published", &config.Service{Ports: []json.RawMessage{json.RawMessage(`"8080:80"`)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCapabilities("podman", map[string]*config.Service{"app": tc.svc})
			if (err != nil) != tc.rejected {
				t.Fatalf("error %v", err)
			}
			if tc.rejected {
				// A nil connection would panic if StartService attempted an SDK operation.
				_, err = (&PodmanEngine{}).StartService(context.Background(), "app", tc.svc, "stack")
				if err == nil || !strings.Contains(err.Error(), "app") {
					t.Fatalf("direct start error %v", err)
				}
			}
		})
	}
	a, _ := DiscoverCapabilities("podman")
	a.NetworkModes[0] = "changed"
	b, _ := DiscoverCapabilities("podman")
	if b.NetworkModes[0] != "host" {
		t.Fatal("mutable discovery snapshot")
	}
	if _, err := DiscoverCapabilities("unknown"); err == nil {
		t.Fatal("unknown engine accepted")
	}
}

func TestNormalizeLogSince(t *testing.T) {
	before := time.Now().Add(-time.Hour)
	value, err := normalizeLogSince("1h")
	if err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	if got.Before(before.Add(-time.Second)) || got.After(before.Add(time.Second)) {
		t.Fatalf("unexpected timestamp %s", got)
	}
	for _, s := range []string{"", "1700000000", "2024-01-01T00:00:00Z"} {
		got, err := normalizeLogSince(s)
		if err != nil || got != s {
			t.Fatal(s, got, err)
		}
	}
}
