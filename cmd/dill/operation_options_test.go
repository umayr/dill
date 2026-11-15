package main

import (
	"testing"

	"github.com/umayr/dill/internal/operation"
)

func TestOperationOptionsSupportMatrix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    commandOptions
		noDeps  bool
		pull    string
		force   bool
		allowed map[string]bool
	}{
		{"no dependencies", commandOptions{}, true, "", false, map[string]bool{"up": true, "start": true, "restart": true}},
		{"pull", commandOptions{}, false, "missing", false, map[string]bool{"up": true}},
		{"force", commandOptions{}, false, "", true, map[string]bool{"teardown": true}},
		{"recreate", commandOptions{ForceRecreate: true}, false, "", false, map[string]bool{"up": true}},
		{"dry run", commandOptions{DryRun: true}, false, "", false, map[string]bool{"up": true, "down": true, "stop": true, "start": true, "restart": true, "rm": true, "teardown": true}},
		{"env file", commandOptions{EnvFile: "private"}, false, "", false, map[string]bool{}},
	} {
		for _, name := range operation.Operations {
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				if got := operationOptionsAllowed(name, tc.opts, tc.noDeps, tc.pull, tc.force); got != tc.allowed[name] {
					t.Fatalf("allowed = %v, want %v", got, tc.allowed[name])
				}
			})
		}
	}
}
