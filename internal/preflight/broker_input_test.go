package preflight

import (
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"strings"
	"testing"
)

func TestBrokerSelectionSeparateDomainAndExactGeneration(t *testing.T) {
	n := sampleSelection(t)
	d := "sha256:" + strings.Repeat("a", 64)
	scope := brokersecret.Scope{Registration: strings.Repeat("b", 32), Stack: n.Stack, Engine: n.Target.Engine, TargetDigest: n.Target.QualifiedIdentityDigest, EndpointDigest: n.Target.EndpointDigest, SourceDigest: d, ConfigDigest: d, PolicyRevision: d, Generation: strings.Repeat("c", 32), BindingRevision: 1}
	s := BrokerSelection{Contract: brokersecret.Version, Scope: scope, Handle: strings.Repeat("d", 32), Named: n}
	first, err := s.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := n.Digest()
	if err != nil || ordinary == first {
		t.Fatal("ordinary digest could authorize broker review")
	}
	for _, change := range []func(*BrokerSelection){func(x *BrokerSelection) { x.Scope.BindingRevision++ }, func(x *BrokerSelection) { x.Scope.Generation = strings.Repeat("e", 32) }, func(x *BrokerSelection) { x.Handle = strings.Repeat("f", 32) }, func(x *BrokerSelection) { x.Scope.SourceDigest = "sha256:" + strings.Repeat("f", 64) }, func(x *BrokerSelection) { x.Scope.ConfigDigest = "sha256:" + strings.Repeat("f", 64) }} {
		next := s
		change(&next)
		digest, err := next.Digest()
		if err != nil || digest == first {
			t.Fatal("review identity not committed")
		}
	}
	s.Scope.TargetDigest = d
	if _, err = s.Digest(); err == nil {
		t.Fatal("target mismatch accepted")
	}
}
func TestBrokerReviewRequiresOpaqueEvidence(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker"}
	if _, err := BrokerConfigInput(cfg, "app", EffectiveFlags{}, nil, &brokersecret.Materialized{}); err == nil {
		t.Fatal("zero evidence effective input")
	}
	if _, err := BrokerContainerDigest("app", "api", "app_api", nil, cfg, &brokersecret.Materialized{}); err == nil {
		t.Fatal("zero evidence protected leaf")
	}
}
