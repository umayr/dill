package core_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/umayr/dill/core"
)

func TestLoadConfigMachineEvaluation(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("POSIX shell required")
	}
	script := `#!/bin/sh
case "$2" in
 invalid) printf '%s' '{"config":{"services":{"synthetic-private":{"image":""}}}}' ;;
 evaluator) printf 'synthetic-private' >&2; exit 1 ;;
 secret-invalid) printf '%s' '{"config":{"name":"demo","secrets":[{"name":"password"}],"services":{"web":{"image":"x","secrets":[{"name":"password","target":"/run/secrets/synthetic-private"}],"labels":{"dill.synthetic-private":"x"}}}}}' ;;
 secret-valid) printf '%s' '{"config":{"name":"demo","secrets":[{"name":"password"}],"services":{"web":{"image":"x","secrets":[{"name":"password","target":"/run/secrets/password"}]}}}}' ;;
 scoped-*) [ -z "${SERVER_SECRET+x}" ] && [ -z "${BRINE_VAR_AMBIENT+x}" ] && [ "$BRINE_VAR_VALUE" = "${2#scoped-}" ] || exit 1; printf '%s' '{"config":{"name":"demo","services":{"web":{"image":"x"}}}}' ;;
 default) [ "$SERVER_SECRET" = present ] || exit 1; printf '%s' '{"config":{"name":"demo","services":{"web":{"image":"x"}}}}' ;;
 *) exit 1 ;;
esac
`
	p := filepath.Join(t.TempDir(), "pkl")
	if err := os.WriteFile(p, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DILL_PKL_PATH", p)
	t.Setenv("SERVER_SECRET", "present")
	t.Setenv("BRINE_VAR_AMBIENT", "present")
	if _, err := core.LoadConfig(context.Background(), "default"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			value := fmt.Sprintf("value-%d=literal", i)
			if _, err := core.LoadConfigWithVars(context.Background(), "scoped-"+value, map[string]string{"BRINE_VAR_VALUE": value}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for _, vars := range []map[string]string{{"PATH": "x"}, {"BRINE_VAR_": "x"}, {"BRINE_VAR_X=Y": "x"}, {"BRINE_VAR_X": "x\x00y"}, {"BRINE_VAR_X": strings.Repeat("x", 4097)}} {
		if _, err := core.LoadConfigWithVars(context.Background(), "scoped", vars); err == nil {
			t.Errorf("accepted invalid variables: %q", vars)
		}
	}
	if _, err := core.LoadConfigWithVars(context.Background(), "secret-valid", nil); !errors.Is(err, core.ErrSecretPlanningUnsupported) {
		t.Fatalf("scoped secret: %v", err)
	}
	if _, err := core.LoadConfigWithVars(context.Background(), "scoped-expected", nil); err == nil {
		t.Fatal("missing variable accepted")
	}
	tooMany := make(map[string]string)
	for i := 0; i < 129; i++ {
		tooMany[fmt.Sprintf("BRINE_VAR_%d", i)] = "x"
	}
	if _, err := core.LoadConfigWithVars(context.Background(), "scoped-x", tooMany); err == nil {
		t.Fatal("too many variables accepted")
	}
	large := make(map[string]string)
	for i := 0; i < 17; i++ {
		large[fmt.Sprintf("BRINE_VAR_%d", i)] = strings.Repeat("x", 4096)
	}
	if _, err := core.LoadConfigWithVars(context.Background(), "scoped-x", large); err == nil {
		t.Fatal("oversized environment accepted")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := core.LoadConfigWithVars(canceled, "scoped-x", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("scoped cancellation: %v", err)
	}
	_, err := core.LoadConfig(context.Background(), "invalid")
	var invalid *core.InvalidConfigError
	if !errors.As(err, &invalid) || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatalf("invalid config diagnostics: %v", err)
	}
	_, err = core.LoadConfig(context.Background(), "evaluator")
	var evaluator *core.EvaluatorError
	if !errors.As(err, &evaluator) || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatalf("evaluator diagnostics: %v", err)
	}
	_, err = core.LoadConfig(context.Background(), "secret-invalid")
	if err == nil || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatalf("secret diagnostics: %v", err)
	}
	_, err = core.LoadConfig(context.Background(), "secret-valid")
	if !errors.Is(err, core.ErrSecretPlanningUnsupported) {
		t.Fatalf("secret config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = core.LoadConfig(ctx, "invalid")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
