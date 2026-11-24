package orchestrator

import (
	"context"
	"github.com/umayr/dill/internal/config"
	"testing"
)

type declarationFixture struct {
	v       *volumeEvidence
	creates int
}

func (f *declarationFixture) inspectDeclaration(context.Context, string) (*volumeEvidence, error) {
	return f.v, nil
}
func (f *declarationFixture) createDeclaration(_ context.Context, n, s string, d config.VolumeDeclaration) error {
	f.creates++
	f.v = &volumeEvidence{n, "local", d.DriverOptions, declarationLabels(s, d)}
	return nil
}
func TestDeclaredVolumeOwnership(t *testing.T) {
	f := new(declarationFixture)
	ctx := context.Background()
	m := config.VolumeDeclaration{Ownership: config.VolumeManaged, Labels: map[string]string{"project": "demo"}}
	x := config.VolumeDeclaration{Ownership: config.VolumeExternal, Name: "db"}
	check := func(d config.VolumeDeclaration, create bool, want string) {
		t.Helper()
		a, err := declared(ctx, f, "db", "stack", d, create)
		if a != want || (err != nil) != (want == "") {
			t.Fatalf("%q %v, want %q", a, err, want)
		}
	}
	check(x, true, "")
	check(m, false, "create managed")
	if f.creates != 0 {
		t.Fatal("preview or external created data")
	}
	check(m, true, "reuse managed")
	check(m, false, "reuse managed")
	f.v.labels["dill.stack"] = "other"
	check(m, true, "")
	check(x, false, "reuse external")
	if f.creates != 1 {
		t.Fatal("unexpected create count")
	}
}
