package brokersecret

import (
	"strings"
	"testing"
)

func TestBrokerRequirementsMetadataValidation(t *testing.T) {
	base := Requirements{Contract: Version, Stack: "app", Engine: "docker", ConfigDigest: "sha256:" + strings.Repeat("a", 64), Names: []string{"password"}, Grants: []Grant{{Service: "web", Name: "password", Target: "/run/secrets/password", Mode: 0400}}}
	if !base.Valid() {
		t.Fatal("valid description refused")
	}
	for _, mutate := range []func(*Requirements){
		func(r *Requirements) { r.Names = []string{"password", "password"} },
		func(r *Requirements) { r.Grants = []Grant{} },
		func(r *Requirements) {
			r.Grants = []Grant{{Service: "web", Name: "different", Target: "/run/secrets/password", Mode: 0400}}
		},
		func(r *Requirements) {
			r.Grants = []Grant{{Service: "web", Name: "password", Target: "/tmp/password", Mode: 0400}}
		},
		func(r *Requirements) {
			r.Grants = []Grant{{Service: "web", Name: "password", Target: "/run/secrets/password", Mode: 0644}}
		},
	} {
		copy := base
		mutate(&copy)
		if copy.Valid() {
			t.Fatal("invalid grant metadata accepted")
		}
	}
	empty := base
	empty.Names = []string{}
	empty.Grants = []Grant{}
	if !empty.Valid() {
		t.Fatal("empty discovery is not unavailable")
	}
}
