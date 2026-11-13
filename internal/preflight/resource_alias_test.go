package preflight

import (
	"strings"
	"testing"
)

// Regression for independent review C4D-1: accurate row counts and individually
// matching private facts cannot authorize two actual names for one container ID.
func TestRejectPresentImmutableIDAliasesAcrossNames(t *testing.T) {
	selection := sampleSelection(t)
	id := strings.Repeat("a", 64)
	leaf := "sha256:" + strings.Repeat("b", 64)
	selection.Resources[0].Identity = id
	selection.Resources[0].IdentityKind = "immutable-id"
	selection.Resources[0].Presence = "present"
	selection.Resources[0].Action = "ensure-started"
	alias := selection.Resources[0]
	alias.Name = "synthetic_alias"
	selection.Resources = append(selection.Resources, alias)
	selection.Coverage.Containers.Count = 2
	selection.Coverage.Containers.PresentCount = 2
	selection.Facts.Containers = []ContainerFact{{Name: "synthetic_web", Exists: true, ID: &id, ConfigDigest: &leaf}, {Name: "synthetic_alias", Exists: true, ID: &id, ConfigDigest: &leaf}}
	r := sampleResult(t)
	r.Resources = selection.Resources
	r.Coverage = selection.Coverage
	if _, err := CoverageFor(r.Resources); err == nil {
		t.Fatal("coverage accepted aliased immutable ID")
	}
	if err := r.Validate(0); err == nil {
		t.Fatal("producer validated two names for one present container")
	}
	if _, err := r.Buffer(0); err == nil {
		t.Fatal("producer buffered aliased identity review")
	}
	if _, err := selection.Digest(); err == nil {
		t.Fatal("matching private facts hid aliased actual identity")
	}
	// Do not collapse rows or reject legitimate equal configurations. Distinct
	// immutable identities with unchanged names/counts/settings must still work.
	other := strings.Repeat("c", 64)
	r.Resources[len(r.Resources)-1].Identity = other
	selection.Facts.Containers[1].ID = &other
	if err := r.Validate(0); err != nil {
		t.Fatal("distinct actual identity rejected", err)
	}
	if _, err := selection.Digest(); err != nil {
		t.Fatal("distinct matching private identity rejected", err)
	}
}
