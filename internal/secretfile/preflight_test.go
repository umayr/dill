package secretfile

import (
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	tests := []struct {
		name     string
		ranges   []IDRange
		id, want int64
		ok       bool
	}{
		{"rootless", []IDRange{{0, 1000, 1}, {1, 100000, 65536}}, 100, 100099, true},
		{"first", []IDRange{{0, 1000, 1}}, 0, 1000, true},
		{"unmapped", []IDRange{{1, 100000, 2}}, 0, 0, false},
		{"ambiguous", []IDRange{{0, 1000, 2}, {0, 2000, 2}}, 0, 0, false},
		{"overflow", []IDRange{{0, ^uint64(0), 2}}, 0, 0, false},
		{"host out of range", []IDRange{{0, 2147483648, 1}}, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := translate(tt.ranges, tt.id)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("got %d, %v", got, ok)
			}
		})
	}
}

func TestParseIDMap(t *testing.T) {
	good, err := ParseIDMap(strings.NewReader("0 1000 1\n1 100000 65536\n"))
	if err != nil || len(good) != 2 {
		t.Fatalf("valid map: %v", err)
	}
	for _, bad := range []string{"", "0 0 0\n", "0 -1 1\n", "0 0 4294967296\n", "0 1000 2\n1 2000 1\n", "0 1000 2\n2 1001 1\n", "0 0 1 extra\n", strings.Repeat(" ", 65537)} {
		if _, err := ParseIDMap(strings.NewReader(bad)); err == nil {
			t.Fatal("invalid map accepted")
		}
	}
}
