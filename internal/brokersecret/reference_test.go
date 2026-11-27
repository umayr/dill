package brokersecret

import (
	"strings"
	"testing"
)

func TestBrokerReferenceMetadataRefusals(t *testing.T) {
	for _, raw := range []string{`{"scope":{},"scope":{}}`, `{"handle":"x","values":["private"]}`, `{"scope":{"stack":"a","stack":"b"}}`, `{} {}`, strings.Repeat(" ", MaxMetadata+1)} {
		var ref Reference
		if DecodeMetadata(strings.NewReader(raw), &ref) == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	var ref Reference
	if DecodeMetadata(strings.NewReader(`{"scope":{},"handle":"x"}`), &ref) != nil || ref.Valid() {
		t.Fatal("syntax validity confused with scoped authorization")
	}
}
