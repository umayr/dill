package dag

import (
	"testing"

	"github.com/umayr/dill/internal/config"
)

func svc(deps ...string) *config.Service {
	return &config.Service{DependsOn: deps}
}

// --- Build ---

func TestBuild_validGraph(t *testing.T) {
	services := map[string]*config.Service{
		"db":  svc(),
		"api": svc("db"),
		"web": svc("api"),
	}
	g, err := Build(services)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.nodes))
	}
}

func TestBuild_unknownDependency(t *testing.T) {
	services := map[string]*config.Service{
		"web": svc("redis"), // redis is not defined
	}
	_, err := Build(services)
	if err == nil {
		t.Error("expected error for unknown dependency")
	}
}

func TestBuild_selfLoop(t *testing.T) {
	services := map[string]*config.Service{
		"web": svc("web"),
	}
	_, err := Build(services)
	if err == nil {
		t.Error("expected error for self-loop cycle")
	}
}

func TestBuild_mutualCycle(t *testing.T) {
	services := map[string]*config.Service{
		"a": svc("b"),
		"b": svc("a"),
	}
	_, err := Build(services)
	if err == nil {
		t.Error("expected error for mutual cycle A→B→A")
	}
}

func TestBuild_longerCycle(t *testing.T) {
	services := map[string]*config.Service{
		"a": svc("b"),
		"b": svc("c"),
		"c": svc("a"),
	}
	_, err := Build(services)
	if err == nil {
		t.Error("expected error for A→B→C→A cycle")
	}
}

// --- detectCycles ---

func TestDetectCycles_linearChain(t *testing.T) {
	g, err := Build(map[string]*config.Service{
		"a": svc(),
		"b": svc("a"),
		"c": svc("b"),
	})
	if err != nil {
		t.Fatalf("linear chain should not have cycles: %v", err)
	}
	if err := detectCycles(g); err != nil {
		t.Errorf("detectCycles on linear chain: %v", err)
	}
}

func TestDetectCycles_diamond(t *testing.T) {
	// a → b, a → c, b → d, c → d — diamond shape, no cycle
	g, err := Build(map[string]*config.Service{
		"a": svc("b", "c"),
		"b": svc("d"),
		"c": svc("d"),
		"d": svc(),
	})
	if err != nil {
		t.Fatalf("diamond graph should be valid: %v", err)
	}
	if err := detectCycles(g); err != nil {
		t.Errorf("detectCycles on diamond: %v", err)
	}
}
