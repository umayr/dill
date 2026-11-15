package preflight

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/config"
)

type fixture struct {
	ExitCode int             `json:"exitCode"`
	Stdout   json.RawMessage `json:"stdout"`
}

func readFixture(t *testing.T, name string) fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}
func sampleResult(t *testing.T) Result {
	t.Helper()
	f := readFixture(t, "preview-up-create.json")
	var r Result
	if err := json.Unmarshal(f.Stdout, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func sampleSelection(t *testing.T) Selection {
	t.Helper()
	b, err := os.ReadFile("testdata/canonical-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Selection struct {
			Payload Selection `json:"payload"`
		} `json:"selection"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v.Selection.Payload
}

func TestFrozenDiscovery(t *testing.T) {
	for _, supported := range []bool{false, true} {
		name := "capabilities-unsupported.json"
		if supported {
			name = "capabilities-supported.json"
		}
		f := readFixture(t, name)
		got, err := json.Marshal(Discovery(supported))
		if err != nil {
			t.Fatal(err)
		}
		var a, b any
		if err = json.Unmarshal(got, &a); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(f.Stdout, &b); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("discovery differs: %s", got)
		}
	}
}
func TestFrozenResultRelations(t *testing.T) {
	names := []string{"preview-up-create.json", "preview-up-recreate.json", "preview-up-config-noop.json", "preview-empty-start.json", "preview-down-absent.json", "preview-limit-failure.json", "preview-metadata-unknown.json", "preview-invalid-contract.json"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f := readFixture(t, name)
			var r Result
			if err := json.Unmarshal(f.Stdout, &r); err != nil {
				t.Fatal(err)
			}
			if err := r.Validate(f.ExitCode); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Buffer(f.ExitCode); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRejectUnconfirmableResult(t *testing.T) {
	tests := map[string]func(*Result){
		"unknown version":      func(r *Result) { r.Version = "dill.preflight/2" },
		"selector-only target": func(r *Result) { r.Target.QualifiedIdentityDigest = "" },
		"metadata unknown":     func(r *Result) { r.CoordinationEffects = "unknown" },
		"truncated":            func(r *Result) { r.ResourcesTruncated = true },
		"nil digest":           func(r *Result) { r.InputDigest = nil },
		"short ID": func(r *Result) {
			r.Resources[0].Presence = "present"
			r.Resources[0].IdentityKind = "immutable-id"
			r.Resources[0].Identity = "0123456789ab"
			r.Resources[0].Action = "ensure-started"
		},
		"invented absent ID":   func(r *Result) { r.Resources[0].Identity = strings.Repeat("a", 64) },
		"volume incarnation":   func(r *Result) { r.Resources[2].IdentityKind = "immutable-id" },
		"bad kind":             func(r *Result) { r.Resources[0].Kind = "foreign" },
		"bad action":           func(r *Result) { r.Resources[0].Action = "remove" },
		"container data claim": func(r *Result) { r.Resources[0].Retention = "preserve" },
		"coverage lie":         func(r *Result) { r.Coverage.Containers.PresentCount = 1 },
		"coverage incomplete":  func(r *Result) { r.Coverage.Containers.Complete = false },
		"duplicate resource":   func(r *Result) { r.Resources = append(r.Resources, r.Resources[0]) },
		"unsafe display":       func(r *Result) { r.Resources[0].Name = "unsafe\nname" },
		"large display":        func(r *Result) { r.Resources[0].Name = strings.Repeat("x", 256) },
		"private diagnostic":   func(r *Result) { r.Errors = []Error{{"PREFLIGHT_UNAVAILABLE", "private engine text"}} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r := sampleResult(t)
			mutate(&r)
			if err := r.Validate(0); err == nil {
				t.Fatal("accepted invalid review")
			}
		})
	}
	r := sampleResult(t)
	if err := r.Validate(1); err == nil {
		t.Fatal("accepted nonzero exit success")
	}
	r.Outcome = "failed"
	r.Errors = []Error{FixedError("PREFLIGHT_UNAVAILABLE")}
	if err := r.Validate(1); err == nil {
		t.Fatal("failure exposed positive review")
	}
	r.Withdraw()
	if err := r.Validate(1); err != nil {
		t.Fatal(err)
	}
}
func TestActionMatrix(t *testing.T) {
	for _, op := range Operations() {
		for _, present := range []bool{false, true} {
			r := Resource{Kind: "network", Name: "stack", Identity: "stack", IdentityKind: "planned-name", Presence: "absent", Action: "ensure", Retention: "preserve"}
			if present {
				r.Identity = strings.Repeat("a", 64)
				r.IdentityKind = "immutable-id"
				r.Presence = "present"
			}
			if op == "stop" || op == "down" {
				r.Action = "retain"
			}
			if err := r.validate(op, "stack"); err != nil {
				t.Fatalf("%s present%v: %v", op, present, err)
			}
		}
	}
	// In particular, empty-stack start/restart may ensure an absent network;
	// that does not grant support for an absent desired container.
	for _, op := range []string{"start", "restart"} {
		r := sampleResult(t).Resources[0]
		r.Action = op
		if err := r.validate(op, "synthetic"); err == nil {
			t.Fatal("absent container became startable")
		}
	}
}
func TestNamesAndMaximumProjection(t *testing.T) {
	if !ValidName(strings.Repeat("x", 255)) || ValidName(strings.Repeat("x", 256)) || ValidName("bad name") || ValidName("a\xff") {
		t.Fatal("unsafe name grammar")
	}
	r := sampleResult(t)
	r.Resources = r.Resources[:0]
	for i := 0; i < 255; i++ {
		name := fmt.Sprintf("v%03d%s", i, strings.Repeat("x", 251))
		r.Resources = append(r.Resources, Resource{"volume", name, name, "planned-name", "absent", "ensure", "preserve"})
	}
	r.Resources = append(r.Resources, Resource{"network", r.Stack, r.Stack, "planned-name", "absent", "ensure", "preserve"})
	var err error
	r.Coverage, err = CoverageFor(r.Resources)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Buffer(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxOutputBytes {
		t.Fatal("unbounded encoding")
	}
	r.Resources = append(r.Resources, Resource{"container", "extra", "extra", "planned-name", "absent", "create", "not-applicable"})
	if _, err = CoverageFor(r.Resources); err == nil {
		t.Fatal("resource limit ignored")
	}
}

func TestCanonicalFrozenVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/canonical-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Store struct {
			KeyHex   string `json:"keyHex"`
			Expected string `json:"privateExpectedTokenHex"`
		} `json:"store"`
		Input struct {
			Payload   Input  `json:"payload"`
			Canonical string `json:"canonicalUtf8"`
			Expected  string `json:"expected"`
		} `json:"input"`
		Selection struct {
			Payload   Selection `json:"payload"`
			Canonical string    `json:"canonicalUtf8"`
			Expected  string    `json:"expected"`
		} `json:"selection"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	got, err := v.Input.Payload.Digest()
	if err != nil || got != v.Input.Expected {
		t.Fatalf("input %s %v", got, err)
	}
	got, err = v.Selection.Payload.Digest()
	if err != nil || got != v.Selection.Expected {
		t.Fatalf("selection %s %v", got, err)
	}
	for _, pair := range []struct {
		value    any
		expected string
	}{{v.Input.Payload, v.Input.Canonical}, {v.Selection.Payload, v.Selection.Canonical}} {
		actual, err := CanonicalJSON(pair.value)
		if err != nil || string(actual) != pair.expected {
			t.Fatalf("canonical bytes differ %s %v", actual, err)
		}
	}
	key, err := hex.DecodeString(v.Store.KeyHex)
	if err != nil {
		t.Fatal(err)
	}
	got, err = StoreScope(key, 11, 22, 1000)
	if err != nil || got != v.Store.Expected {
		t.Fatalf("store %s %v", got, err)
	}
	for _, coords := range [][3]uint64{{12, 22, 1000}, {11, 23, 1000}, {11, 22, 1001}} {
		different, err := StoreScope(key, coords[0], coords[1], coords[2])
		if err != nil || different == got {
			t.Fatal("store identity not bound")
		}
	}
	key[0] ^= 1
	different, _ := StoreScope(key, 11, 22, 1000)
	if different == got {
		t.Fatal("store key not bound")
	}
}
func TestCanonicalRestrictions(t *testing.T) {
	a, _ := CanonicalJSON(map[string]any{"z": "<&>", "a": []any{1, "é"}})
	if string(a) != `{"a":[1,"é"],"z":"\u003c\u0026\u003e"}` {
		t.Fatalf("unexpected canonical %s", a)
	}
	if _, err := CanonicalJSON(map[string]any{"float": 1.5}); err == nil {
		t.Fatal("floating canonical input")
	}
	if _, err := StoreScope(make([]byte, 31), 1, 2, 3); err == nil {
		t.Fatal("short private key")
	}
}
func TestSelectionCommitmentChangesAndPrivateFacts(t *testing.T) {
	base := sampleSelection(t)
	original, err := base.Digest()
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Selection){
		"selector":           func(s *Selection) { s.Target.EndpointDigest = "sha256:" + strings.Repeat("8", 64) },
		"qualified identity": func(s *Selection) { s.Target.QualifiedIdentityDigest = "sha256:" + strings.Repeat("8", 64) },
		"store":              func(s *Selection) { s.StateScope = strings.Repeat("8", 64) },
		"effective input":    func(s *Selection) { s.InputDigest = "sha256:" + strings.Repeat("8", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			s := sampleSelection(t)
			mutate(&s)
			got, err := s.Digest()
			if err != nil || got == original {
				t.Fatalf("changed scope not bound %s %v", got, err)
			}
		})
	}
	s := sampleSelection(t)
	s.Facts.Containers = nil
	if _, err = s.Digest(); err == nil {
		t.Fatal("missing private facts accepted")
	}
	s = sampleSelection(t)
	id := strings.Repeat("a", 64)
	s.Facts.Containers[0].ID = &id
	if _, err = s.Digest(); err == nil {
		t.Fatal("invented absent ID accepted")
	}
}
func TestOriginalDesiredHashProvenanceIsNotCollapsed(t *testing.T) {
	a := &config.Service{Image: "local/image:one", PullPolicy: "never"}
	b := &config.Service{Image: "local/image:two", PullPolicy: "never"}
	ha, err := config.ServiceConfigHash("synthetic", "web", a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := config.ServiceConfigHash("synthetic", "web", b)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatal("original desired provenance collapsed")
	}
	input := Input{LogicalContract: Version, Engine: "docker", Stack: "synthetic", Operation: "up", Flags: EffectiveFlags{TimeoutNanos: 1, EffectivePullPolicy: "never", CallerSource: "brine"}, Services: []ServiceInput{{Name: "web", ContainerName: "synthetic_web", DesiredConfigHash: ha, DependsOn: []string{}, EffectivePullPolicy: "never", PreparedImageID: "sha256:" + strings.Repeat("a", 64)}}}
	first, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	input.Services[0].DesiredConfigHash = hb
	second, err := input.Digest()
	if err != nil || first == second {
		t.Fatal("same prepared ID collapsed different desired input")
	}
}
