package preflight

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func invocationFixtures(t *testing.T) ([][]string, []string) {
	t.Helper()
	data, err := os.ReadFile("testdata/argv.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Positive []struct {
			Argv []string `json:"argv"`
		} `json:"positive"`
		Suffix []string `json:"bindingSuffix"`
	}
	if err = json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	out := [][]string{}
	for _, row := range f.Positive {
		out = append(out, append([]string{}, row.Argv[1:]...))
	}
	return out, f.Suffix
}
func TestFrozenNamedArgvAndBoundApply(t *testing.T) {
	rows, suffix := invocationFixtures(t)
	for _, args := range rows {
		v, code := ParseInvocation(args, false)
		if code != "" || v.Operation != args[0] {
			t.Fatalf("frozen argv failed %v %s", args, code)
		}
		apply := append([]string{}, args...)
		for i, token := range apply {
			if token == Version {
				apply[i] = "dill.operation/1"
			}
		}
		apply = append(apply, suffix...)
		bound, code := ParseInvocation(apply, true)
		if code != "" || bound.ExpectedPreflight != suffix[len(suffix)-1] {
			t.Fatalf("bound argv failed %s", code)
		}
	}
}
func replaceValue(args []string, name, value string) []string {
	copy := append([]string{}, args...)
	i := slices.Index(copy, name)
	copy[i+1] = value
	return copy
}
func removeFlag(args []string, name string) []string {
	copy := append([]string{}, args...)
	i := slices.Index(copy, name)
	return append(copy[:i], copy[i+2:]...)
}
func TestNamedArgvClosedFlagsAndDuplicates(t *testing.T) {
	rows, _ := invocationFixtures(t)
	base := rows[0]
	for name, extra := range map[string][]string{
		"duplicate f": {"-f", "x"}, "duplicate stack alias": {"--project-name", "synthetic"}, "same duplicate value": {"--engine=docker"},
		"internal semantic stack not alias": {"--stack", "synthetic"}, "unfrozen config alias": {"--file", "x"},
		"services": {"web"}, "bare separator": {"--"}, "triple dash": {"---quiet"}, "no deps": {"--no-deps=false"},
		"dry false": {"--dry-run=false"}, "yes false": {"--yes=false"}, "short yes": {"-y"}, "force": {"--force=false"},
		"disposable": {"--alpha-disposable=false"}, "env": {"--env-file", "x"}, "secret": {"--secret-target", "x"},
		"binding preview": {"--expect-preflight", "sha256:" + string(make([]byte, 64))}, "unknown": {"--unknown"},
		"bool separate": {"--quiet", "false"}, "invalid bool": {"--quiet=notbool"},
	} {
		t.Run(name, func(t *testing.T) {
			args := append(append([]string{}, base...), extra...)
			if _, code := ParseInvocation(args, false); code == "" {
				t.Fatal("unsupported flag/operand accepted")
			}
		})
	}
	for _, flag := range []string{"--force-recreate=false", "--no-prune=false", "--pull=never"} {
		args := append(append([]string{}, rows[1]...), flag)
		if _, code := ParseInvocation(args, false); code == "" {
			t.Fatal("up-only flag on start accepted")
		}
	}
	for _, flag := range []string{"-f", "-p", "--engine", "--engine-socket", "--coordination-layout", "--coordination-daemon-id", "--coordination-state-dir", "--coordination-source"} {
		if _, code := ParseInvocation(removeFlag(base, flag), false); code == "" {
			t.Fatal("implicit tuple field accepted", flag)
		}
	}
}
func TestNamedArgvValuesAreNotFlagTokensAndGoBooleans(t *testing.T) {
	rows, _ := invocationFixtures(t)
	for _, value := range []string{"TRUE", "true", "1", "T", "t", "FALSE", "false", "0", "F", "f"} {
		args := append(append([]string{}, rows[0]...), "--quiet="+value)
		if _, code := ParseInvocation(args, false); code != "" {
			t.Fatal("Go bool spelling rejected", value, code)
		}
	}
	for _, value := range []string{"relative--dry-run.pkl", "--dry-run", "--expect-preflight"} {
		args := replaceValue(rows[0], "-f", value)
		v, code := ParseInvocation(args, false)
		if code != "" || v.ConfigFile != value {
			t.Fatal("flag-looking string value reinterpreted", code)
		}
	}
	alias := append([]string{}, rows[0]...)
	for i, v := range alias {
		if v == "-p" {
			alias[i] = "--stack-name"
		}
		if v == "--engine" {
			alias[i] = "-engine"
		}
	}
	if _, code := ParseInvocation(alias, false); code != "" {
		t.Fatal("frozen alias/single dash rejected", code)
	}
}
func TestNamedArgvInvalidSemanticsAndBinding(t *testing.T) {
	rows, suffix := invocationFixtures(t)
	base := rows[0]
	for flag, values := range map[string][]string{"--contract": {"", "future"}, "--format": {"yaml", ""}, "--engine": {"podman", ""}, "--engine-socket": {"", "tcp://localhost:2375", "unix://relative"}, "--coordination-layout": {"containerized", ""}, "--coordination-state-dir": {"relative", ""}, "--coordination-source": {"ambient", ""}, "--timeout": {"0", "-1s", "invalid"}, "--pull": {"always", "missing"}} {
		for _, value := range values {
			if _, code := ParseInvocation(replaceValue(base, flag, value), false); code == "" {
				t.Fatal("invalid semantic accepted", flag, value)
			}
		}
	}
	apply := replaceValue(base, "--contract", "dill.operation/1")
	apply = append(apply, suffix...)
	for _, args := range [][]string{removeFlag(apply, "--preflight-contract"), removeFlag(apply, "--expect-preflight"), replaceValue(apply, "--preflight-contract", "future"), replaceValue(apply, "--expect-preflight", "sha256:sha256:bad"), removeFlag(apply, "--coordination-daemon-id")} {
		if _, code := ParseInvocation(args, true); code == "" {
			t.Fatal("partial/unqualified binding accepted")
		}
	}
	if _, code := ParseInvocation(nil, false); code != "UNSUPPORTED_OPERATION" {
		t.Fatal("missing action accepted")
	}
}
