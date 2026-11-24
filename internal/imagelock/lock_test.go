package imagelock

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseAndResolve(t *testing.T) {
	digest := "docker.io/library/app@sha256:" + strings.Repeat("a", 64)
	data := fmt.Sprintf(`{"version":1,"services":{"web":{"image":"app:latest","digest":%q}},"jobs":{}}`, digest)
	l, err := Parse([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := l.Resolve(map[string]string{"web": "app:latest"}, nil)
	if err != nil || s["web"] != digest {
		t.Fatalf("resolve: %v %v", s, err)
	}
	for _, input := range []map[string]string{nil, {"web": "app:other"}, {"other": "app:latest"}} {
		if _, _, err := l.Resolve(input, nil); err == nil {
			t.Fatalf("accepted stale entries: %v", input)
		}
	}
}
func TestRejectInvalid(t *testing.T) {
	for _, s := range []string{
		`{"version":1,"version":1,"services":{},"jobs":{}}`,
		`{"version":2,"services":{},"jobs":{}}`,
		`{"version":1,"services":{},"jobs":{},"extra":1}`,
		`{"version":1,"services":{},"jobs":{}} {}`,
		`{"version":1,"services":{"x":{"image":"app","digest":"app:latest"}},"jobs":{}}`,
		`{"version":1,"services":null,"jobs":{}}`,
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
