package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseSecretBindings(t *testing.T) {
	valid := `{"version":"dill.secret-bindings/1","target":"lab-a","stack":"demo","bindings":[{"name":"db-password","source":"db-prod","revision":"r2"}]}`
	got, err := ParseSecretBindings(strings.NewReader(valid))
	if err != nil || got.Target != "lab-a" || len(got.Bindings) != 1 || got.Bindings[0].Revision != "r2" {
		t.Fatalf("valid binding: %#v, %v", got, err)
	}
	cases := map[string]string{
		"unknown":              strings.Replace(valid, `"target":"lab-a"`, `"target":"lab-a","sentinel":"secret-value"`, 1),
		"duplicate key":        strings.Replace(valid, `"target":"lab-a"`, `"target":"lab-a","target":"lab-a"`, 1),
		"duplicate nested key": strings.Replace(valid, `"name":"db-password"`, `"name":"db-password","name":"db-password"`, 1),
		"missing":              strings.Replace(valid, `"target":"lab-a",`, "", 1),
		"null":                 strings.Replace(valid, `"bindings":[`, `"bindings":null,"extra":[`, 1),
		"trailing object":      valid + `{}`,
		"duplicate name":       strings.Replace(valid, `}]}`, `},{"name":"db-password","source":"other","revision":"r1"}]}`, 1),
		"bad name":             strings.Replace(valid, `db-password`, `DB-password`, 1),
		"oversize":             valid + strings.Repeat(" ", 64*1024),
		"invalid utf8":         valid + string([]byte{0xff}),
		"long string":          strings.Replace(valid, `lab-a`, strings.Repeat("a", 129), 1),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseSecretBindings(strings.NewReader(input))
			if !errors.Is(err, ErrSecretUnavailable) || strings.Contains(err.Error(), "sentinel") || strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("expected fixed secret error, got %v", err)
			}
		})
	}
}

func TestParseSecretBindingsEntryLimit(t *testing.T) {
	for _, n := range []int{256, 257} {
		entries := make([]string, n)
		for i := range entries {
			entries[i] = fmt.Sprintf(`{"name":"n%d","source":"a","revision":"r"}`, i)
		}
		input := `{"version":"dill.secret-bindings/1","target":"a","stack":"a","bindings":[` + strings.Join(entries, ",") + `]}`
		if len(input) >= 65536 {
			t.Fatal("fixture exceeds byte limit")
		}
		got, err := ParseSecretBindings(strings.NewReader(input))
		if n == 256 && (err != nil || len(got.Bindings) != 256) {
			t.Fatalf("256: %v", err)
		}
		if n == 257 && !errors.Is(err, ErrSecretUnavailable) {
			t.Fatalf("257: %v", err)
		}
	}
}
