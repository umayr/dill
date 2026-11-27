package brokersecret

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestRetirementExplicitContractAndScope(t *testing.T) {
	envelope, _ := envelopeFixture(t)
	defer envelope.Close()
	scope := envelope.Scope
	old := scope
	old.Generation = strings.Repeat("f", 32)
	scope.BindingRevision = old.BindingRevision + 1
	r := Retirement{Contract: RetirementVersion, Scope: scope, Previous: []Reference{{Scope: old, Handle: strings.Repeat("b", 32)}}}
	raw, err := EncodeRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRetirement(bytes.NewReader(raw))
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatal("retirement codec mismatch", err)
	}
	for _, change := range []func(*Retirement){
		func(r *Retirement) { r.Contract = Version },
		func(r *Retirement) { r.Previous = nil },
		func(r *Retirement) { r.Previous = append(r.Previous, r.Previous[0]) },
		func(r *Retirement) { r.Previous[0].Scope.Registration = strings.Repeat("d", 32) },
		func(r *Retirement) { r.Previous[0].Scope.Stack = "other" },
		func(r *Retirement) { r.Previous[0].Scope.TargetDigest = Digest([]byte("other")) },
		func(r *Retirement) { r.Previous[0].Scope.EndpointDigest = Digest([]byte("other")) },
		func(r *Retirement) { r.Previous[0].Scope.Generation = r.Scope.Generation },
		func(r *Retirement) { r.Previous[0].Scope.BindingRevision = r.Scope.BindingRevision + 1 },
	} {
		bad := r
		bad.Previous = append([]Reference(nil), r.Previous...)
		change(&bad)
		if _, e := EncodeRetirement(bad); e == nil {
			t.Fatal("invalid predecessor admitted")
		}
	}
	for _, bad := range []string{strings.Replace(string(raw), `"previous":`, `"unexpected":true,"previous":`, 1), strings.Replace(string(raw), `"contract":`, `"contract":"duplicate","contract":`, 1)} {
		if _, err := DecodeRetirement(strings.NewReader(bad)); err == nil {
			t.Fatal("loose metadata accepted")
		}
	}
	if _, err := DecodeRetirement(nil); err == nil {
		t.Fatal("nil accepted")
	}
	if _, err := Decode(bytes.NewReader(raw)); err == nil {
		t.Fatal("retirement became an empty ordinary envelope")
	}
}
