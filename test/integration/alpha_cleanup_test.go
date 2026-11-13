//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type alphaFixtureCommand func(...string) (string, error)

func alphaCleanupCommand(engine string) alphaFixtureCommand {
	return func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, engineBinaryName(engine), args...)
		cmd.Env = os.Environ()
		data, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(data)), err
	}
}

func alphaFixtureCleanupArgs(kind, identity string) ([]string, []string, error) {
	if identity == "" || strings.HasPrefix(identity, "-") || strings.ContainsAny(identity, " \t\r\n\x00|") {
		return nil, nil, errors.New("invalid exact fixture cleanup identity")
	}
	switch kind {
	case "container":
		return []string{"ps", "-a", "--no-trunc", "--format", "{{.ID}}|{{.Names}}"}, []string{"rm", "-f", identity}, nil
	case "network":
		return []string{"network", "ls", "--no-trunc", "--format", "{{.ID}}|{{.Name}}"}, []string{"network", "rm", identity}, nil
	case "volume":
		return []string{"volume", "ls", "--format", "{{.Name}}"}, []string{"volume", "rm", identity}, nil
	case "image":
		return []string{"image", "ls", "--no-trunc", "--format", "{{.ID}}|{{.Repository}}:{{.Tag}}"}, []string{"rmi", identity}, nil
	default:
		return nil, nil, errors.New("unsupported fixture cleanup namespace")
	}
}

func alphaRemoveFixture(kind, identity string, run alphaFixtureCommand) error {
	list, remove, err := alphaFixtureCleanupArgs(kind, identity)
	if err != nil {
		return err
	}
	exists := func() (bool, error) {
		data, err := run(list...)
		if err != nil {
			return false, fmt.Errorf("fixture cleanup inventory failed (%T; raw diagnostics withheld)", err)
		}
		for _, row := range strings.Split(data, "\n") {
			if row == "" {
				continue
			}
			fields := strings.Split(row, "|")
			expected := 2
			if kind == "volume" {
				expected = 1
			}
			if len(fields) != expected {
				return false, errors.New("malformed fixture cleanup inventory")
			}
			for _, field := range fields {
				if field == identity {
					return true, nil
				}
			}
		}
		return false, nil
	}
	present, err := exists()
	if err != nil {
		return err
	}
	if !present {
		// Only a successful explicit inventory proves intentional already-absent.
		// An arbitrary nonzero inspect/remove exit is NEVER absence evidence.
		return nil
	}
	if _, err := run(remove...); err != nil {
		return fmt.Errorf("exact fixture removal failed (%T; raw diagnostics withheld)", err)
	}
	present, err = exists()
	if err != nil {
		return err
	}
	if present {
		return errors.New("exact fixture cleanup did not converge")
	}
	return nil
}

func alphaCleanupResource(t *testing.T, engine, kind, identity string) {
	t.Helper()
	if err := alphaRemoveFixture(kind, identity, alphaCleanupCommand(engine)); err != nil {
		t.Errorf("scoped %s fixture cleanup: %v", kind, err)
	}
}

func alphaScopedCleanupConverged(stack string, run alphaFixtureCommand) error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(stack) {
		return errors.New("invalid synthetic stack cleanup scope")
	}
	for _, args := range [][]string{
		{"ps", "-aq", "--filter", "label=dill.stack=" + stack},
		{"network", "ls", "-q", "--filter", "label=dill.stack=" + stack},
		{"volume", "ls", "-q", "--filter", "label=dill.stack=" + stack},
	} {
		data, err := run(args...)
		if err != nil {
			return fmt.Errorf("scoped cleanup convergence inventory failed (%T; raw diagnostics withheld)", err)
		}
		if strings.TrimSpace(data) != "" {
			return errors.New("scoped synthetic stack resources remain after cleanup")
		}
	}
	return nil
}

func alphaVerifyScopedCleanup(t *testing.T, engine, stack string) {
	t.Helper()
	if err := alphaScopedCleanupConverged(stack, alphaCleanupCommand(engine)); err != nil {
		t.Errorf("scoped fixture cleanup convergence: %v", err)
	}
}

func TestAlphaFixtureCleanupClassification(t *testing.T) {
	// Pure helper regression, NOT real engine cleanup evidence.
	expected := map[string][][]string{
		"container": {{"ps", "-a", "--no-trunc", "--format", "{{.ID}}|{{.Names}}"}, {"rm", "-f", "synthetic-exact-fixture"}},
		"network":   {{"network", "ls", "--no-trunc", "--format", "{{.ID}}|{{.Name}}"}, {"network", "rm", "synthetic-exact-fixture"}},
		"volume":    {{"volume", "ls", "--format", "{{.Name}}"}, {"volume", "rm", "synthetic-exact-fixture"}},
		"image":     {{"image", "ls", "--no-trunc", "--format", "{{.ID}}|{{.Repository}}:{{.Tag}}"}, {"rmi", "synthetic-exact-fixture"}},
	}
	for _, kind := range []string{"container", "network", "volume", "image"} {
		for _, scenario := range []string{"absent", "inventory-error", "remove-error", "still-present", "removed"} {
			t.Run(kind+"/"+scenario, func(t *testing.T) {
				const identity = "synthetic-exact-fixture"
				list, remove := expected[kind][0], expected[kind][1]
				present, removals := scenario != "absent", 0
				run := func(args ...string) (string, error) {
					if reflect.DeepEqual(args, list) {
						if scenario == "inventory-error" {
							return "", errors.New("synthetic-unavailable")
						}
						if !present {
							return "", nil
						}
						if kind == "volume" {
							return identity, nil
						}
						return "synthetic-full-id|" + identity, nil
					}
					if !reflect.DeepEqual(args, remove) {
						t.Fatal("cleanup broadened exact removal or used an unexpected command")
					}
					removals++
					if scenario == "remove-error" {
						return "", errors.New("synthetic-denied")
					}
					present = scenario == "still-present"
					return "", nil
				}
				err := alphaRemoveFixture(kind, identity, run)
				wantError := scenario != "absent" && scenario != "removed"
				if (err != nil) != wantError {
					t.Fatal("cleanup error/absence/convergence classification incorrect")
				}
				wantRemovals := 1
				if scenario == "absent" || scenario == "inventory-error" {
					wantRemovals = 0
				}
				if removals != wantRemovals {
					t.Fatal("cleanup did not stay scoped to one existing exact fixture")
				}
			})
		}
	}
	for _, identity := range []string{"", "--all", "contains\nnewline"} {
		if err := alphaRemoveFixture("container", identity, func(...string) (string, error) { t.Fatal("invalid identity executed engine command"); return "", nil }); err == nil {
			t.Fatal("unsafe cleanup identity accepted")
		}
	}
	for _, kind := range []string{"unsupported", ""} {
		if err := alphaRemoveFixture(kind, "synthetic", func(...string) (string, error) { t.Fatal("invalid scope executed engine command"); return "", nil }); err == nil {
			t.Fatal("unsupported cleanup scope accepted")
		}
	}
}

func TestAlphaFixtureCleanupScopeConvergence(t *testing.T) {
	for _, scenario := range []string{"empty", "unexpected-resource", "inventory-error"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			err := alphaScopedCleanupConverged("synthetic-stack", func(args ...string) (string, error) {
				calls++
				expected := [][]string{
					{"ps", "-aq", "--filter", "label=dill.stack=synthetic-stack"},
					{"network", "ls", "-q", "--filter", "label=dill.stack=synthetic-stack"},
					{"volume", "ls", "-q", "--filter", "label=dill.stack=synthetic-stack"},
				}
				if calls > len(expected) || !reflect.DeepEqual(args, expected[calls-1]) {
					t.Fatal("cleanup convergence query broadened fixture scope or mutated resources")
				}
				if scenario == "inventory-error" {
					return "", errors.New("synthetic-unavailable")
				}
				if scenario == "unexpected-resource" {
					return "synthetic-resource", nil
				}
				return "", nil
			})
			wantCalls := 1
			if scenario == "empty" {
				wantCalls = 3
			}
			if (err == nil) != (scenario == "empty") || calls != wantCalls {
				t.Fatal("cleanup convergence accepted leftovers or unavailable inventory")
			}
		})
	}
}
