package protocol

import (
	"bytes"
	"strings"
	"testing"
)

const goodRequest = `{"protocolVersion":"dill.dev/1","operation":"protocol.check","requiredFeatures":[]}`
const fixtureID = "op_0123456789abcdef0123456789abcdef"

func TestParseStrict(t *testing.T) {
	tests := []struct {
		name, input string
		want        Code
	}{
		{"valid", goodRequest, ""},
		{"features", strings.Replace(goodRequest, `[]`, `["ndjson","resultAxes"]`, 1), ""},
		{"escaped key", strings.Replace(goodRequest, `operation`, `oper\u0061tion`, 1), ""},
		{"empty", "", InvalidRequest}, {"null", "null", InvalidRequest}, {"array", "[]", InvalidRequest},
		{"case", strings.Replace(goodRequest, `operation`, `Operation`, 1), InvalidRequest},
		{"unknown", strings.Replace(goodRequest, `"operation"`, `"SENTINEL":"SENTINEL","operation"`, 1), InvalidRequest},
		{"duplicate", strings.Replace(goodRequest, `"operation"`, `"operation":"protocol.check","oper\u0061tion"`, 1), InvalidRequest},
		{"duplicate nested", strings.Replace(goodRequest, `[]`, `[{"x":1,"x":2}]`, 1), InvalidRequest},
		{"null field", strings.Replace(goodRequest, `[]`, `null`, 1), InvalidRequest},
		{"null array", strings.Replace(goodRequest, `[]`, `[null]`, 1), InvalidRequest},
		{"wrong type", strings.Replace(goodRequest, `[]`, `{}`, 1), InvalidRequest},
		{"number", strings.Replace(goodRequest, `"dill.dev/1"`, `1`, 1), InvalidRequest},
		{"missing", `{"protocolVersion":"dill.dev/1","operation":"protocol.check"}`, InvalidRequest},
		{"trailing", goodRequest + ` {}`, InvalidRequest}, {"garbage", goodRequest + `X`, InvalidRequest},
		{"comment", goodRequest + ` //hi`, InvalidRequest}, {"truncated", goodRequest[:20], InvalidRequest},
		{"bom", "\xef\xbb\xbf" + goodRequest, InvalidRequest},
		{"invalid UTF8", strings.Replace(goodRequest, `[]`, "[\"\xff\"]", 1), InvalidRequest},
		{"high surrogate", strings.Replace(goodRequest, `[]`, `["\ud800"]`, 1), InvalidRequest},
		{"low surrogate", strings.Replace(goodRequest, `[]`, `["\udc00"]`, 1), InvalidRequest},
		{"wrong pair", strings.Replace(goodRequest, `[]`, `["\ud800\u0041"]`, 1), InvalidRequest},
		{"valid pair", strings.Replace(goodRequest, `[]`, `["\ud83d\ude00"]`, 1), UnsupportedFeature},
		{"replacement literal", strings.Replace(goodRequest, `[]`, `["�"]`, 1), UnsupportedFeature},
		{"escaped backslash", strings.Replace(goodRequest, `[]`, `["\\ud800"]`, 1), UnsupportedFeature},
		{"duplicate features", strings.Replace(goodRequest, `[]`, `["ndjson","ndjson"]`, 1), InvalidRequest},
		{"unsupported feature", strings.Replace(goodRequest, `[]`, `["SENTINEL"]`, 1), UnsupportedFeature},
		{"unknown version", strings.Replace(goodRequest, Version, "SENTINEL", 1), UnsupportedProtocol},
		{"unknown op", strings.Replace(goodRequest, Operation, "SENTINEL", 1), UnsupportedOperation},
		{"string limit", strings.Replace(goodRequest, `[]`, `["`+strings.Repeat("a", 65)+`"]`, 1), InvalidRequest},
		{"unicode byte limit", strings.Replace(goodRequest, `[]`, `["`+strings.Repeat("é", 33)+`"]`, 1), InvalidRequest},
		{"features limit", strings.Replace(goodRequest, `[]`, `["a","b","c","d","e","f","g","h","i"]`, 1), InvalidRequest},
		{"deep", strings.Replace(goodRequest, `[]`, `[[[[[]]]]]`, 1), InvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := Parse([]byte(tt.input))
			if got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
func TestParseBoundsAndPrecedence(t *testing.T) {
	for _, n := range []int{MaxRequest - 1, MaxRequest, MaxRequest + 1} {
		b := append([]byte(goodRequest), bytes.Repeat([]byte{' '}, n-len(goodRequest))...)
		_, code := Parse(b)
		want := Code("")
		if n > MaxRequest {
			want = RequestTooLarge
		}
		if code != want {
			t.Fatalf("size %d: %s", n, code)
		}
	}
	for _, n := range []int{63, 64, 65} {
		_, code := Parse([]byte(strings.Replace(goodRequest, `[]`, `["`+strings.Repeat("x", n)+`"]`, 1)))
		want := UnsupportedFeature
		if n > 64 {
			want = InvalidRequest
		}
		if code != want {
			t.Fatalf("string %d: %s", n, code)
		}
	}
	if !strictJSON([]byte(`[[[[]]]]`), 4, 64) || strictJSON([]byte(`[[[[[]]]]]`), 4, 64) {
		t.Fatal("depth boundary")
	}
	// Schema rejection precedes unsupported version, then version/op/features.
	_, code := Parse([]byte(`{"protocolVersion":"other","operation":"other","requiredFeatures":["other"],"x":0}`))
	if code != InvalidRequest {
		t.Fatal(code)
	}
	_, code = Parse([]byte(`{"protocolVersion":"other","operation":"other","requiredFeatures":["other"]}`))
	if code != UnsupportedProtocol {
		t.Fatal(code)
	}
}
func FuzzParseNoInputEcho(f *testing.F) {
	for _, s := range []string{goodRequest, `null`, `{"x":"\ud800"}`, `{"x":1,"x":2}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		_, code := Parse(b)
		if code != "" {
			encoded, err := Encode(failure(code))
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > MaxRecord {
				t.Fatal("unbounded failure")
			}
		}
	})
}
