package brokersecret

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetirementManifestCannotBecomeOrdinaryEmpty(t *testing.T) {
	e, _ := envelopeFixture(t)
	defer e.Close()
	old := e.Scope
	next := old
	next.Generation = strings.Repeat("f", 32)
	next.BindingRevision++
	request := Retirement{Contract: RetirementVersion, Scope: next, Previous: []Reference{{Scope: old, Handle: strings.Repeat("b", 32)}}}
	m := manifest{Contract: RetirementVersion, Review: Review{Scope: next}, Retirement: &request}
	if !validManifestKind(m) {
		t.Fatal("explicit retirement rejected")
	}
	bad := m
	bad.Contract = Version
	if validManifestKind(bad) {
		t.Fatal("ordinary contract admitted empty retirement")
	}
	bad = m
	bad.Files = []storedFile{{}}
	if validManifestKind(bad) {
		t.Fatal("retirement included file grants")
	}
	bad = m
	bad.Retirement = nil
	if validManifestKind(bad) {
		t.Fatal("empty metadata admitted without previous scope")
	}
	ordinary := manifest{Contract: Version, Files: []storedFile{{}}}
	if !validManifestKind(ordinary) {
		t.Fatal("ordinary metadata rejected")
	}
	raw, err := json.Marshal(ordinary)
	if err != nil || strings.Contains(string(raw), "retirement") {
		t.Fatal("ordinary manifest encoding changed")
	}
	material := &Materialized{store: &Store{}, review: m.Review, retirement: &request}
	if _, _, err := material.References(nil, false); err == nil {
		t.Fatal("retirement accepted without exact evaluated config and store evidence", err)
	}
}
