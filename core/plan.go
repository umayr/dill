package core

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

// Config and Service describe evaluated Dill configuration. These types are
// shared with the CLI; callers constructing them must provide normalized Pkl
// equivalents, including BaseDir for relative mounts.
type Config = config.DillConfig
type VolumeDeclaration = config.VolumeDeclaration
type VolumeOwnership = config.VolumeOwnership

const (
	VolumeManaged  = config.VolumeManaged
	VolumeExternal = config.VolumeExternal
)

// BindVolumes validates volume declarations in a programmatically built config.
func BindVolumes(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	return config.BindVolumes(cfg)
}

type Service = config.Service
type HealthCheck = config.HealthCheck
type SecretRequirement = config.SecretRequirement
type SecretGrant = config.SecretGrant

// LiveConfig is the engine's observed container configuration.
type LiveConfig = orchestrator.LiveConfig

// ErrNotFound signals that an inspected container does not exist.
var ErrNotFound = orchestrator.ErrNotFound

// PlanSource is the read-only capability required to compute a plan.
type PlanSource = plan.Source

// Plan, Change, FieldDiff and ChangeKind describe the shared CLI plan.
type Plan = plan.Plan
type Change = plan.Change
type FieldDiff = plan.FieldDiff
type ChangeKind = plan.ChangeKind

const (
	Create   = plan.KindCreate
	Recreate = plan.KindRecreate
	Remove   = plan.KindRemove
	Noop     = plan.KindNoop
)

// ErrSecretPlanningUnsupported indicates that the public planning API cannot
// authorize file-secret delivery. It never falls back to unchecked planning.
var ErrSecretPlanningUnsupported = errors.New("file-secret planning is unsupported by core")

// InvalidConfigError and EvaluatorError classify fixed, redacted failures from
// LoadConfig. Other failures may also be returned without evaluator details.
type InvalidConfigError = config.InvalidError
type EvaluatorError = config.EvaluatorError

// LoadConfig evaluates Pkl using bounded machine evaluation and runtime
// validation; diagnostics are redacted. File-secret configurations are
// deliberately unsupported. Pkl inherits the process environment and may use
// the filesystem and network: this is not a sandbox. For request-scoped Brine
// variables without ambient inheritance, use LoadConfigWithVars instead; do
// not mutate the process environment to configure individual requests.
func LoadConfig(ctx context.Context, path string) (*Config, error) {
	cfg, err := config.Load(config.WithMachineEvaluation(ctx), path)
	if err != nil {
		return nil, err
	}
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return nil, ErrSecretPlanningUnsupported
	}
	return cfg, nil
}

// LoadConfigWithVars evaluates with exactly the supplied BRINE_VAR_ environment;
// no ambient variables are inherited. The trusted caller resolves shared/stack
// precedence before calling. Names must be BRINE_VAR_ followed by uppercase
// ASCII letters, digits or underscores, with at least one suffix character.
// At most 128 entries, 128 bytes per name, 4096 UTF-8 bytes per value, and
// 65536 total name/value bytes are accepted; values may not contain NUL.
// These bounds are stricter than Brine's variable store (16 KiB per value and
// 1 MiB per document); an integration must surface rejected inputs rather than
// assume every valid Brine document can be passed here. The caller must not
// mutate vars during this call. Scoped evaluation is unavailable on Windows,
// where Go can inject SYSTEMROOT into a subprocess environment.
//
// Executable discovery still uses the operator's process environment. The
// restricted child may not work with configs requiring ambient environment,
// proxies, or particular cache settings; there is no fallback. Pkl is not a
// sandbox: evaluated code may access the filesystem and network with the
// process's permissions. Diagnostics remain bounded and redacted, and file
// secret delivery is rejected exactly as in LoadConfig.
func LoadConfigWithVars(ctx context.Context, path string, vars map[string]string) (*Config, error) {
	if err := validateScopedPlatform(ctx, runtime.GOOS); err != nil {
		return nil, err
	}
	if len(vars) > 128 {
		return nil, errors.New("invalid scoped variables")
	}
	keys := make([]string, 0, len(vars))
	total := 0
	for k, v := range vars {
		if !strings.HasPrefix(k, "BRINE_VAR_") || len(k) <= len("BRINE_VAR_") || len(k) > 128 || len(v) > 4096 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return nil, errors.New("invalid scoped variables")
		}
		for _, c := range k[len("BRINE_VAR_"):] {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return nil, errors.New("invalid scoped variables")
			}
		}
		total += len(k) + len(v)
		if total > 65536 {
			return nil, errors.New("invalid scoped variables")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+vars[k])
	}
	ctx = config.WithScopedEnvironment(config.WithMachineEvaluation(ctx), env)
	cfg, err := config.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return nil, ErrSecretPlanningUnsupported
	}
	return cfg, nil
}

func validateScopedPlatform(ctx context.Context, goos string) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if goos == "windows" {
		return errors.New("scoped Pkl evaluation is unsupported on Windows")
	}
	return nil
}

// ComputePlan computes the same ordinary read-only plan as the CLI. It does
// not authorize file-secret plans or deployments. The source remains owned by
// the caller. Stack selection and endpoint authorization are caller duties.
func ComputePlan(ctx context.Context, cfg *Config, source PlanSource, stack string) (*Plan, error) {
	if cfg == nil || source == nil {
		return nil, errors.New("config and plan source are required")
	}
	if config.RejectUncheckedSecretDelivery(cfg) != nil {
		return nil, ErrSecretPlanningUnsupported
	}
	if err := config.ValidateRuntime(cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if err := config.ValidateContainerNames(cfg.Services, stack); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return plan.Compute(ctx, cfg, source, stack)
}

// PlanEngine is a read-only planning source that owns its connection.
type PlanEngine interface {
	PlanSource
	Close() error
}

// OpenPlanEngine opens an explicit Docker or Podman endpoint without selecting
// a process-global default. Endpoint authorization is the caller's duty.
func OpenPlanEngine(ctx context.Context, engine, endpoint string) (PlanEngine, error) {
	if endpoint == "" {
		return nil, errors.New("plan engine endpoint is required")
	}
	switch engine {
	case "docker":
		return orchestrator.NewDockerEngineAt(endpoint)
	case "podman":
		return orchestrator.NewPodmanEngine(ctx, endpoint)
	default:
		return nil, fmt.Errorf("unknown engine %q", engine)
	}
}
