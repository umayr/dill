package config

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const policyFixture = `{"version":"dill.secret-policy/1","targets":[{"id":"lab-a","engine":"docker","socket":"/run/docker.sock","stacks":[{"name":"demo","sources":[{"id":"db-prod","revision":"r2","kind":"hostFile","path":"/srv/secrets/db-r2","uid":1000,"gid":1000,"mode":256}]}]}]}`

func secretFixture() (*DillConfig, *SecretBindings) {
	return &DillConfig{Name: "demo", Secrets: []SecretRequirement{{Name: "db"}}, Services: map[string]*Service{"app": {Image: "img", Secrets: []SecretGrant{{Name: "db", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256}}}}}, &SecretBindings{Version: "dill.secret-bindings/1", Target: "lab-a", Stack: "demo", Bindings: []SecretBinding{{Name: "db", Source: "db-prod", Revision: "r2"}}}
}
func TestSecretPolicyStrictAndAuthorize(t *testing.T) {
	p, err := ParseSecretPolicy(strings.NewReader(policyFixture))
	if err != nil {
		t.Fatal(err)
	}
	cfg, b := secretFixture()
	r, err := AuthorizeSecrets(cfg, b, p, "lab-a", "demo", "docker", "/run/docker.sock")
	if err != nil || r["app"][0].HostPath != "/srv/secrets/db-r2" {
		t.Fatalf("resolution: %#v %v", r, err)
	}
	for _, input := range []string{strings.Replace(policyFixture, `"id":"lab-a"`, `"id":"lab-a","bad":"sentinel"`, 1), strings.Replace(policyFixture, `"id":"lab-a"`, `"id":"lab-a","id":"lab-a"`, 1), strings.Replace(policyFixture, `"mode":256`, `"mode":null`, 1), policyFixture + ` {}`, strings.Replace(policyFixture, `"kind":"hostFile"`, `"kind":"native"`, 1)} {
		if _, err := ParseSecretPolicy(strings.NewReader(input)); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("accepted invalid policy: %v", err)
		}
	}
	for _, tc := range []struct{ target, stack, engine, socket string }{{"lab-b", "demo", "docker", "/run/docker.sock"}, {"lab-a", "other", "docker", "/run/docker.sock"}, {"lab-a", "demo", "podman", "/run/docker.sock"}, {"lab-a", "demo", "docker", "/tmp/sock"}} {
		if _, err := AuthorizeSecrets(cfg, b, p, tc.target, tc.stack, tc.engine, tc.socket); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("cross scope accepted %+v", tc)
		}
	}
	b.Bindings[0].Revision = "r3"
	if _, err := AuthorizeSecrets(cfg, b, p, "lab-a", "demo", "docker", "/run/docker.sock"); err == nil {
		t.Fatal("unauthorized revision")
	}
}
func TestValidateSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DillConfig)
	}{
		{"name", func(c *DillConfig) { c.Secrets[0].Name = "Bad" }},
		{"duplicate declaration", func(c *DillConfig) { c.Secrets = append(c.Secrets, c.Secrets[0]) }},
		{"reference", func(c *DillConfig) { c.Services["app"].Secrets[0].Name = "other" }},
		{"duplicate grant", func(c *DillConfig) { s := c.Services["app"]; s.Secrets = append(s.Secrets, s.Secrets[0]) }},
		{"traversal", func(c *DillConfig) { c.Services["app"].Secrets[0].Target = "/run/secrets/../x" }},
		{"outside", func(c *DillConfig) { c.Services["app"].Secrets[0].Target = "/run/secrets" }},
		{"uid", func(c *DillConfig) { c.Services["app"].Secrets[0].UID = 2147483648 }},
		{"gid", func(c *DillConfig) { c.Services["app"].Secrets[0].GID = -1 }},
		{"mode", func(c *DillConfig) { c.Services["app"].Secrets[0].Mode = 384 }},
		{"mount ancestor", func(c *DillConfig) { c.Services["app"].Volumes = []json.RawMessage{json.RawMessage(`"x:/run"`)} }},
		{"mount descendant", func(c *DillConfig) {
			c.Services["app"].Volumes = []json.RawMessage{json.RawMessage(`"x:/run/secrets/db/a"`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := secretFixture()
			tc.change(c)
			if err := ValidateSecrets(c); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	c, _ := secretFixture()
	if err := ValidateSecrets(c); err != nil {
		t.Fatal(err)
	}
}
func TestSecretServiceConfigHash(t *testing.T) {
	c, b := secretFixture()
	p, _ := ParseSecretPolicy(strings.NewReader(policyFixture))
	r, err := AuthorizeSecrets(c, b, p, "lab-a", "demo", "docker", "/run/docker.sock")
	if err != nil {
		t.Fatal(err)
	}
	svc := c.Services["app"]
	h, err := SecretServiceConfigHash("demo", "app", svc, "lab-a", r["app"])
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ResolvedSecret){func(x *ResolvedSecret) { x.Source = "other" }, func(x *ResolvedSecret) { x.Revision = "r3" }, func(x *ResolvedSecret) { x.HostPath = "/other" }} {
		copyR := append([]ResolvedSecret(nil), r["app"]...)
		change(&copyR[0])
		h2, e := SecretServiceConfigHash("demo", "app", svc, "lab-a", copyR)
		if e != nil {
			t.Fatal(e)
		}
		if (copyR[0].HostPath == "/other") != (h == h2) {
			t.Fatalf("unexpected hash %s %s", h, h2)
		}
	}
	h2, _ := SecretServiceConfigHash("demo", "app", svc, "lab-b", r["app"])
	if h == h2 {
		t.Fatal("target unchanged")
	}
	empty := &Service{Image: "img"}
	legacy, _ := ServiceConfigHash("demo", "app", empty)
	extended, _ := SecretServiceConfigHash("demo", "app", empty, "", nil)
	if legacy != extended {
		t.Fatal("legacy hash changed")
	}
}

func TestSecretHashOrderAndGrantChanges(t *testing.T) {
	c, _ := secretFixture()
	svc := c.Services["app"]
	svc.Secrets = append(svc.Secrets, SecretGrant{Name: "api", Target: "/run/secrets/api", UID: 1, GID: 2, Mode: 288})
	refs := []ResolvedSecret{{Name: "db", Source: "s1", Revision: "r1", Target: "/run/secrets/db", UID: 1000, GID: 1000, Mode: 256}, {Name: "api", Source: "s2", Revision: "r2", Target: "/run/secrets/api", UID: 1, GID: 2, Mode: 288}}
	h, err := SecretServiceConfigHash("demo", "app", svc, "lab-a", refs)
	if err != nil {
		t.Fatal(err)
	}
	svc.Secrets[0], svc.Secrets[1] = svc.Secrets[1], svc.Secrets[0]
	refs[0], refs[1] = refs[1], refs[0]
	other, err := SecretServiceConfigHash("demo", "app", svc, "lab-a", refs)
	if err != nil || other != h {
		t.Fatalf("reordering changed hash: %v", err)
	}
	svc.Secrets[0].Mode = 256
	refs[0].Mode = 256
	other, err = SecretServiceConfigHash("demo", "app", svc, "lab-a", refs)
	if err != nil || h == other {
		t.Fatalf("mode did not change hash: %v", err)
	}
	svc.Secrets[0].Target = "/run/secrets/other"
	refs[0].Target = svc.Secrets[0].Target
	third, err := SecretServiceConfigHash("demo", "app", svc, "lab-a", refs)
	if err != nil || third == other {
		t.Fatalf("grant target did not change hash: %v", err)
	}
}
func TestSecretJSONShape(t *testing.T) {
	raw := []byte(`{"name":"demo","services":{"app":{"image":"alpine","secrets":[{"name":"db","target":"/run/secrets/db"}]}},"secrets":[{"name":"db"}]}`)
	var c DillConfig
	if err := decodeStrict(raw, &c); err != nil {
		t.Fatal(err)
	}
	if err := validateDecodedShape(raw, &c); err != nil {
		t.Fatal(err)
	}
	if c.Services["app"].Secrets[0].Mode != 256 {
		t.Fatal("default mode")
	}
	if err := ValidateRuntime(&c); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(string(raw), `"name":"db","target"`, `"target"`, 1), strings.Replace(string(raw), `"target":"/run/secrets/db"`, `"target":"/run/secrets/db","bad":1`, 1), strings.Replace(string(raw), `"secrets":[{"name":"db"}]`, `"secrets":null`, 1), strings.Replace(string(raw), `"target":"/run/secrets/db"`, `"target":"/run/secrets/db","mode":null`, 1)} {
		var x DillConfig
		err := decodeStrict([]byte(bad), &x)
		if err == nil {
			err = validateDecodedShape([]byte(bad), &x)
		}
		if err == nil {
			t.Fatalf("accepted invalid shape %s", bad)
		}
	}
}

func TestSecretPolicyDuplicateScopes(t *testing.T) {
	source := `{"id":"db-prod","revision":"r2","kind":"hostFile","path":"/srv/secrets/db-r2","uid":1000,"gid":1000,"mode":256}`
	stack := `{"name":"demo","sources":[` + source + `]}`
	target := `{"id":"lab-a","engine":"docker","socket":"/run/docker.sock","stacks":[` + stack + `]}`
	docs := []string{
		`{"version":"dill.secret-policy/1","targets":[` + target + `,` + target + `]}`,
		`{"version":"dill.secret-policy/1","targets":[{"id":"lab-a","engine":"docker","socket":"/run/docker.sock","stacks":[` + stack + `,` + stack + `]}]}`,
		`{"version":"dill.secret-policy/1","targets":[{"id":"lab-a","engine":"docker","socket":"/run/docker.sock","stacks":[{"name":"demo","sources":[` + source + `,` + source + `]}]}]}`,
	}
	for _, doc := range docs {
		if _, err := ParseSecretPolicy(strings.NewReader(doc)); !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("duplicate scope accepted: %v", err)
		}
	}
}
