package brokersecret

import (
	"strings"
	"testing"
)

func TestRetirementLivePredecessorSetIsExplicit(t *testing.T) {
	e, _ := envelopeFixture(t)
	defer e.Close()
	next := e.Scope
	next.Generation = strings.Repeat("f", 32)
	next.BindingRevision++
	old := Reference{Scope: e.Scope, Handle: strings.Repeat("b", 32)}
	request := Retirement{Contract: RetirementVersion, Scope: next, Previous: []Reference{old}}
	m := &Materialized{review: Review{Handle: strings.Repeat("c", 32), Scope: next}, retirement: &request}
	if !m.permitsLive(old.Handle, old.Scope.Generation) || !m.permitsLive(m.review.Handle, next.Generation) {
		t.Fatal("exact previous/current not accepted")
	}
	if m.permitsLive(strings.Repeat("d", 32), old.Scope.Generation) || m.permitsLive(old.Handle, next.Generation) {
		t.Fatal("unlisted/mixed generation accepted")
	}
	request.Previous = nil
	if m.permitsLive(m.review.Handle, next.Generation) {
		t.Fatal("invalid retirement request admitted")
	}
}
