package brokersecret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func scopeFixture() Scope {
	d := "sha256:" + strings.Repeat("a", 64)
	return Scope{Registration: strings.Repeat("b", 32), Stack: "app", Engine: "docker", TargetDigest: d, SourceDigest: d, ConfigDigest: d, EndpointDigest: d, BindingRevision: 1, PolicyRevision: d, Generation: strings.Repeat("c", 32)}
}
func envelopeFixture(t *testing.T) (Envelope, []Grant) {
	t.Helper()
	item, e := NewItem("password", []byte("synthetic-private-target-value"))
	if e != nil {
		t.Fatal(e)
	}
	return Envelope{Scope: scopeFixture(), Items: []Item{item}}, []Grant{{Service: "api", Name: "password", Target: "/run/secrets/password", UID: 1000, GID: 1000, Mode: 0400}}
}
func TestWireContractAndRedaction(t *testing.T) {
	e, g := envelopeFixture(t)
	if err := e.Validate(e.Scope, g); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{e, e.Items[0], struct{ private Item }{e.Items[0]}} {
		if strings.Contains(fmt.Sprintf("%#v", v), "synthetic-private") {
			t.Fatal("formatted plaintext")
		}
	}
	if _, err := json.Marshal(e); err == nil {
		t.Fatal("general marshal allowed")
	}
	wire, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Decode(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if next.Validate(e.Scope, g) != nil || !bytes.Equal(next.Items[0].Bytes(), e.Items[0].Bytes()) {
		t.Fatal("wire mismatch")
	}
	next.Close()
	if len(next.Items) != 0 {
		t.Fatal("closed envelope retained items")
	}
}
func TestContractRefusals(t *testing.T) {
	e, g := envelopeFixture(t)
	wrong := e.Scope
	wrong.BindingRevision++
	if e.Validate(wrong, g) == nil {
		t.Fatal("scope mismatch")
	}
	for _, grant := range []Grant{{Service: "api", Name: "password", Target: "/etc/password", Mode: 0400}, {Service: "api", Name: "password", Target: "/run/secrets/../elsewhere", Mode: 0400}, {Service: "api", Name: "password", Target: "/run/secrets/password", Mode: 0600}, {Service: "api", Name: "missing", Target: "/run/secrets/missing", Mode: 0400}} {
		if e.Validate(e.Scope, []Grant{grant}) == nil {
			t.Fatal("unsafe grant")
		}
	}
	if e.Validate(e.Scope, append(g, g...)) == nil {
		t.Fatal("duplicate grants")
	}
	wire, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(wire), `"contract":`, `"contract":"other","contract":`, 1), strings.Replace(string(wire), `"name":"password"`, `"name":"password","name":"other"`, 1), strings.Replace(string(wire), `"name":"password"`, `"name":"password","unexpected":true`, 1), strings.Replace(string(wire), `"data":"c3ludGhldGljLXByaXZhdGUtdGFyZ2V0LXZhbHVl"`, `"data":null`, 1), string(wire) + `{}`} {
		if _, err := Decode(strings.NewReader(bad)); err == nil {
			t.Fatal("unsafe wire accepted")
		}
	}
}
func TestExplicitEmptyValueNotMissing(t *testing.T) {
	item, err := NewItem("empty", nil)
	if err != nil {
		t.Fatal(err)
	}
	e := Envelope{Scope: scopeFixture(), Items: []Item{item}}
	wire, err := Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"data":""`)) {
		t.Fatal("empty must be explicit")
	}
	if _, err = Decode(bytes.NewReader(wire)); err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(strings.NewReader(strings.Replace(string(wire), `"data":""`, `"data":null`, 1))); err == nil {
		t.Fatal("null accepted")
	}
}
