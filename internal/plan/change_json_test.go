package plan

import (
	"encoding/json"
	"testing"
)

func TestChangeKindJSONRoundTrip(t *testing.T) {
	for _, want := range []ChangeKind{KindCreate, KindRecreate, KindRemove, KindNoop} {
		raw, err := json.Marshal(Change{Service: "example", Kind: want})
		if err != nil {
			t.Fatal(err)
		}
		var got Change
		if json.Unmarshal(raw, &got) != nil || got.Kind != want {
			t.Fatal("native plan kind cannot cross agent boundary")
		}
	}
	for _, raw := range []string{"null", "0", "1", `"unknown"`, `"CREATE"`, `{}`, `[]`} {
		got := KindRemove
		if json.Unmarshal([]byte(raw), &got) == nil || got != KindRemove {
			t.Fatal("invalid kind accepted or changed destination")
		}
	}
}
