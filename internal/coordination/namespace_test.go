package coordination

import (
	"strings"
	"testing"
)

func rootlessProfile() NamespaceProfile {
	return NamespaceProfile{Rootless: true, UIDMap: []IDMap{{0, 1000, 1}, {1, 100000, 65536}}, GIDMap: []IDMap{{0, 1000, 1}, {1, 100000, 65536}}}
}
func TestNamespaceProfileMappingAndCommitment(t *testing.T) {
	p := rootlessProfile()
	if !p.Valid() || !p.Owners(0, 0) || !p.Owners(65536, 65536) || p.Owners(65537, 0) || p.Owners(0, 65537) {
		t.Fatal("incorrect mapped-owner admission")
	}
	a, _ := p.Digest()
	q := rootlessProfile()
	q.UIDMap[0], q.UIDMap[1] = q.UIDMap[1], q.UIDMap[0]
	b, _ := q.Digest()
	if a != b {
		t.Fatal("map ordering altered commitment")
	}
	q.UIDMap[0].Outside++
	b, _ = q.Digest()
	if a == b {
		t.Fatal("mapping drift retained commitment")
	}
	bad := rootlessProfile()
	bad.UIDMap[1].Outside = 0
	if bad.Valid() {
		t.Fatal("rootless map exposed host root UID")
	}
	bad = rootlessProfile()
	bad.GIDMap[1].Outside = 0
	if bad.Valid() {
		t.Fatal("rootless map exposed host root GID")
	}
	p.Rootless = false
	if p.Valid() {
		t.Fatal("rootless maps admitted as rootful")
	}
	rootful := NamespaceProfile{UIDMap: []IDMap{{0, 0, uint32(maxID)}}, GIDMap: []IDMap{{0, 0, uint32(maxID)}}}
	if !rootful.Valid() || !rootful.Owners(uint32(maxID-1), 0) || rootful.Owners(uint32(maxID), 0) {
		t.Fatal("rootful ID boundary incorrect")
	}
}
func TestIDMapParserDenials(t *testing.T) {
	for _, raw := range []string{"", "0 1000 0\n", "0 1000 2\n1 10000 1\n", "0 1000 1\n1 1000 2\n", "4294967295 0 1\n", "0 4294967295 1\n", "0 0 4294967296\n", "-1 0 1\n", "+0 0 1\n", "0 0 1 extra\n", "0 0 1\n\n", "0\u00a00 1\n", strings.Repeat(" ", MaxIDMapBytes+1)} {
		if _, err := ParseIDMap([]byte(raw)); err == nil {
			t.Fatal("invalid ID map admitted")
		}
	}
	m, err := ParseIDMap([]byte("         0       1000          1\n         1     100000      65536\n"))
	if err != nil || len(m) != 2 {
		t.Fatal("kernel map refused")
	}
	if _, err = ParseIDMap([]byte("0 0 4294967295\n")); err != nil {
		t.Fatal("initial namespace refused")
	}
}
func TestNamespaceEvidenceRequiresExactNamespaceAndMaps(t *testing.T) {
	p := rootlessProfile()
	a := NamespaceEvidence{ID: NamespaceID{1, 2}, Profile: p}
	b := NamespaceEvidence{ID: NamespaceID{1, 2}, Profile: rootlessProfile()}
	if MatchNamespaceEvidence(p, a, b) != nil {
		t.Fatal("matching evidence refused")
	}
	b.ID.Inode++
	if MatchNamespaceEvidence(p, a, b) == nil {
		t.Fatal("same maps substituted for same namespace")
	}
	b.ID = a.ID
	b.Profile.UIDMap[1].Outside++
	if MatchNamespaceEvidence(p, a, b) == nil {
		t.Fatal("mapping drift accepted")
	}
	if MatchNamespaceEvidence(p, NamespaceEvidence{}, b) == nil {
		t.Fatal("missing namespace accepted")
	}
	if CheckNamespacePeer(nil, p) == nil {
		t.Fatal("missing live connection accepted")
	}
}
