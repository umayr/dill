package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

type operationState struct {
	recorder     *operation.Recorder
	config       *config.DillConfig
	declined     bool
	engine       *operation.Engine
	endpoint     string
	coordination *operationCoordination
	census       map[string]string
	listed       []string
	ctx          context.Context
}

func (s *operationState) release() error {
	err := s.releaseEngine()
	if s.coordination != nil {
		err = errors.Join(err, s.coordination.close())
	}
	return err
}

func (s *operationState) releaseEngine() error {
	if s.engine == nil {
		return nil
	}
	engine := s.engine
	s.engine = nil
	return engine.Engine.Close()
}

func operationSupported(name string) bool {
	for _, supported := range operation.Operations {
		if name == supported {
			return true
		}
	}
	return false
}

func runOperationCapabilities(args []string) error {
	fs := flag.NewFlagSet("operation-capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "output format: json")
	if fs.Parse(args) != nil || *format != "json" || fs.NArg() != 0 {
		return protocolExit{2}
	}
	features := []string{"explicitLocalSelector", "attended", "redactedSummary", "conservativeEffects"}
	qOperations, qEngines, qLayouts := []string{}, []string{}, []string{}
	if qualifiedNativeHost() {
		features = append(features, "optInQualifiedLocalCoordination")
		qOperations, qEngines, qLayouts = qualifiedOperations, qualifiedEngines, qualifiedLayouts
	}
	err := json.NewEncoder(os.Stdout).Encode(struct {
		Version                         string   `json:"version"`
		Operations                      []string `json:"operations"`
		Engines                         []string `json:"engines"`
		Features                        []string `json:"features"`
		QualifiedCoordinationOperations []string `json:"qualifiedCoordinationOperations"`
		QualifiedCoordinationEngines    []string `json:"qualifiedCoordinationEngines"`
		QualifiedCoordinationLayouts    []string `json:"qualifiedCoordinationLayouts"`
	}{operation.Version, operation.Operations, []string{"docker", "podman"}, features, qOperations, qEngines, qLayouts})
	if err != nil {
		return protocolExit{1}
	}
	return nil
}

// runOperation is a separate, bounded CLI adapter, not a protocol extension.
// All negotiation/argument validation precedes evaluation or engine access.
func runOperation(ctx context.Context, args []string) error {
	restore := silenceSecretLogs()
	defer restore()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return protocolExit{1}
	}
	result := operation.Result{Version: operation.Version, OperationID: "op_" + hex.EncodeToString(id[:]), Outcome: "invalid_input", Effects: "none", Resources: []operation.Resource{}, Errors: []operation.Error{}}
	state := &operationState{recorder: &operation.Recorder{}}
	defer state.release()
	emit := func(code int, errorCode, message string) error {
		closeErr := state.releaseEngine()
		if state.coordination != nil {
			closeErr = errors.Join(closeErr, state.coordination.closeTransport())
		}
		if closeErr != nil {
			result.Outcome = "unknown"
			code = 1
			errorCode = "ENGINE_CLOSE_FAILED"
			message = "Engine connection release failed. Inspect the target before retrying."
		}
		if errorCode != "" {
			result.Errors = append(result.Errors, operation.Error{Code: errorCode, Message: message})
		}
		state.recorder.Finish(&result)
		if result.Effects == "unknown" && result.Outcome == "failed" {
			result.Outcome = "unknown"
		}
		if result.Outcome == "unknown" {
			result.Effects = "unknown"
		}
		if state.coordination != nil {
			if err := state.coordination.finish(&result); err != nil {
				result.Outcome, result.Effects = "unknown", "unknown"
				result.Errors = append(result.Errors, operation.Error{Code: "COORDINATION_COMMIT_FAILED", Message: "Durable outcome unavailable. Inspect coordination state before retrying."})
				code = 1
			}
		}
		if state.release() != nil {
			// Effects/outcome were already durably finalized. A local lock/fd
			// cleanup error is not new uncertainty about engine mutations; do
			// not manufacture stdout-only unknown axes after a known commit.
			code = 1
			result.Errors = append(result.Errors, operation.Error{Code: "COORDINATION_RELEASE_FAILED", Message: "Engine outcome was recorded; local coordination cleanup failed."})
		}
		if json.NewEncoder(os.Stdout).Encode(result) != nil {
			return protocolExit{1}
		}
		if code != 0 {
			return protocolExit{code}
		}
		return nil
	}
	if secretInvocation(args) {
		result.Outcome = "failed"
		return emit(1, "SECRET_UNAVAILABLE", "Secret configuration or source unavailable.")
	}
	if len(args) == 0 || !operationSupported(args[0]) {
		return emit(2, "UNSUPPORTED_OPERATION", "Unsupported operation. Use operation-capabilities discovery.")
	}
	name := args[0]
	result.Operation = name
	opts := commandOptions{ConfigFile: "compose.pkl", Timeout: 5 * time.Minute, operation: state}
	fs := flag.NewFlagSet("operation", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	fs.BoolVar(&opts.ForceRecreate, "force-recreate", false, "force recreate containers")
	fs.BoolVar(&opts.NoPrune, "no-prune", false, "retain omitted services during up")
	fs.BoolVar(&opts.AlphaDisposable, "alpha-disposable", false, "restrict up to the disposable-volume Alpha profile")
	contract := fs.String("contract", "", "required operation contract version")
	format := fs.String("format", "json", "output format: json")
	noDeps := fs.Bool("no-deps", false, "omit dependencies")
	pull := fs.String("pull", "", "pull policy override")
	force := fs.Bool("force", false, "force image removal")
	coordOptions := coordinationOptions{}
	coordOptions.flags(fs)
	fs.SetOutput(io.Discard)
	if fs.Parse(args[1:]) != nil {
		return emit(2, "INVALID_ARGUMENTS", "Invalid operation arguments.")
	}
	guardrailFlag := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "no-prune" || f.Name == "alpha-disposable" {
			guardrailFlag = true
		}
	})
	if guardrailFlag && name != "up" {
		return emit(2, "INVALID_ARGUMENTS", "Unsupported options for this operation.")
	}
	if *contract != operation.Version {
		return emit(2, "UNSUPPORTED_CONTRACT", "Unsupported operation contract. Use operation-capabilities discovery.")
	}
	if *format != "json" {
		return emit(2, "UNSUPPORTED_FORMAT", "Unsupported operation format.")
	}
	if opts.secretMode() {
		result.Outcome = "failed"
		return emit(1, "SECRET_UNAVAILABLE", "Secret configuration or source unavailable.")
	}
	if opts.proofRequested() || opts.EnvFile != "" || (*noDeps && name != "up" && name != "start" && name != "restart") || (*pull != "" && name != "up") || (*force && name != "teardown") || (opts.ForceRecreate && name != "up") || (opts.DryRun && (name == "validate" || name == "plan" || name == "diff")) {
		return emit(2, "INVALID_ARGUMENTS", "Unsupported options for this operation.")
	}
	if *pull != "" && *pull != "always" && *pull != "missing" && *pull != "never" {
		return emit(2, "INVALID_ARGUMENTS", "Invalid pull policy.")
	}
	if (name == "validate" || name == "plan" || name == "diff" || name == "teardown") && fs.NArg() != 0 {
		return emit(2, "INVALID_ARGUMENTS", "Unexpected operation arguments.")
	}
	if opts.Timeout <= 0 || !opts.StackNameSet || config.ValidateSecretStackName(opts.StackName) != nil || !opts.EngineSocketSet || (opts.Engine != "docker" && opts.Engine != "podman") {
		return emit(2, "INVALID_TARGET", "Explicit local engine, socket and valid stack are required.")
	}
	endpoint, err := localEngineSocket(opts)
	if err != nil || endpoint == "" {
		return emit(2, "INVALID_TARGET", "Explicit local engine, socket and valid stack are required.")
	}
	opts.EngineSocket = endpoint
	if coordOptions.requested() {
		if err := coordOptions.validate(name, opts); err != nil {
			return emit(2, "UNSUPPORTED_COORDINATION", "Unsupported qualified coordination invocation.")
		}
		state.coordination = &operationCoordination{options: coordOptions}
	}
	result.Target = &operation.Target{Engine: opts.Engine, EndpointDigest: operation.Digest(endpoint)}
	result.Stack = opts.StackName
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	state.ctx = ctx
	if ctx.Err() != nil {
		result.Outcome = "cancelled"
		return emit(1, "CANCELLED", "Operation cancelled or deadline exceeded.")
	}
	cfg, err := loadConfig(ctx, opts)
	// Decoded secret grants are deliberately unsupported in this new adapter.
	// No identifying metadata from this path is returned on failure.
	if hasSecretGrants(cfg) || errors.Is(err, config.ErrSecretUnavailable) {
		result.Target = nil
		result.Stack = ""
		result.Outcome = "failed"
		return emit(1, "SECRET_UNAVAILABLE", "Secret configuration or source unavailable.")
	}
	if err == nil {
		if validationErr := config.ValidateRuntime(cfg); validationErr != nil {
			err = &config.InvalidError{Err: validationErr}
		}
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		if _, selectionErr := selectServices(cfg.Services, fs.Args(), false); selectionErr != nil {
			err = selectionErr
		}
	}
	if err == nil {
		state.config = cfg
		opts.Quiet = true
		handlerArgs := append([]string{}, fs.Args()...)
		if *noDeps {
			handlerArgs = append([]string{"--no-deps"}, handlerArgs...)
		}
		if *pull != "" {
			handlerArgs = append([]string{"--pull", *pull}, handlerArgs...)
		}
		if *force {
			handlerArgs = append([]string{"--force"}, handlerArgs...)
		}
		if state.coordination != nil {
			err = state.coordination.prepare(ctx, opts, cfg, name, *pull)
		}
		if err == nil && name != "validate" && name != "plan" && name != "diff" {
			err = preflightOperationOwnership(ctx, opts, cfg)
		}
		if err == nil && (name == "down" || name == "rm" || name == "teardown") {
			err = recordOperationRemovalIntent(ctx, opts, cfg, name, fs.Args())
		}
		if err == nil {
			switch name {
			case "validate":
				err = runValidate(ctx, opts, nil)
			case "plan":
				err = runPlan(ctx, opts, nil)
			case "diff":
				err = runDiff(ctx, opts, nil)
			case "up":
				err = runUp(ctx, opts, handlerArgs)
			case "down":
				err = runDown(ctx, opts, handlerArgs)
			case "stop":
				err = runStop(ctx, opts, handlerArgs)
			case "start":
				err = runStart(ctx, opts, handlerArgs)
			case "restart":
				err = runRestart(ctx, opts, handlerArgs)
			case "rm":
				err = runRm(ctx, opts, handlerArgs)
			case "teardown":
				err = runTeardown(ctx, opts, handlerArgs)
			}
		}
	}
	switch {
	case err == nil && state.declined:
		result.Outcome = "cancelled"
		return emit(0, "", "")
	case err == nil:
		result.Outcome = "succeeded"
		return emit(0, "", "")
	case errors.Is(err, errPlanHasChanges):
		result.Outcome = "changes"
		if name == "diff" {
			return emit(0, "", "")
		}
		return emit(1, "", "")
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		result.Outcome = "cancelled"
		return emit(1, "CANCELLED", "Operation cancelled or deadline exceeded.")
	case coordinationInspection(err):
		result.Outcome = "failed"
		return emit(1, "COORDINATION_INSPECTION_REQUIRED", "Unresolved coordination state. Inspect and explicitly acknowledge it before retrying.")
	case failureKind(err) == "invalid_input":
		result.Outcome = "invalid_input"
		return emit(2, "INVALID_INPUT", "Invalid operation input.")
	default:
		result.Outcome = "failed"
		return emit(1, "OPERATION_FAILED", "Operation failed. Inspect the target before retrying if effects are uncertain.")
	}
}

// Census all declared and stack-listed container identities before any lifecycle
// writes. Reuse the invocation-owned engine for this census and every handler;
// individual handlers still revalidate immutable mutation targets independently.
func preflightOperationOwnership(ctx context.Context, opts commandOptions, cfg *config.DillConfig) error {
	engine, err := newEngine(ctx, cfg.Engine, opts)
	if err != nil {
		return err
	}
	stack := resolveStackNameWithOptions(cfg, opts)
	expected := map[string]string{}
	for service, svc := range cfg.Services {
		expected[serviceContainerName(stack, service, svc)] = service
	}
	listed, err := engine.ListStack(ctx, stack)
	if err != nil {
		return err
	}
	for _, name := range listed {
		if _, exists := expected[name]; !exists {
			expected[name] = ""
		}
	}
	names := make([]string, 0, len(expected))
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)
	captured := map[string]string{}
	for _, name := range names {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		id, err := orchestrator.OwnedID(ctx, engine, name, stack, expected[name])
		if err != nil {
			return err
		}
		captured[name] = id
	}
	opts.operation.census = captured
	opts.operation.listed = append([]string{}, listed...)
	return nil
}

// Publish only opaque preflight candidates. Existing handlers still decide
// whether each candidate is applicable and repeat their own ownership checks.
func recordOperationRemovalIntent(ctx context.Context, opts commandOptions, cfg *config.DillConfig, name string, services []string) error {
	state := opts.operation
	names := append([]string{}, state.listed...)
	if len(services) > 0 {
		names = nil
		for _, service := range services {
			names = append(names, serviceContainerName(opts.StackName, service, cfg.Services[service]))
		}
	}
	sort.Strings(names)
	action := "stopRemove"
	if name == "rm" {
		action = "removeStoppedCandidate"
	}
	for _, container := range names {
		if id := state.census[container]; id != "" {
			state.recorder.Intent("container", id, action)
		}
	}
	if name != "teardown" {
		return nil
	}
	images := map[string]bool{}
	for _, svc := range cfg.Services {
		images[svc.Image] = true
	}
	imageNames := make([]string, 0, len(images))
	for image := range images {
		imageNames = append(imageNames, image)
	}
	sort.Strings(imageNames)
	for _, image := range imageNames {
		id, err := state.engine.LocalImageID(ctx, image)
		if err != nil {
			return err
		}
		if id != "" {
			state.recorder.Intent("image", id, "removeCandidate")
		}
	}
	volumes, err := state.engine.ListStackVolumes(ctx, opts.StackName)
	if err != nil {
		return err
	}
	sort.Strings(volumes)
	for _, volume := range volumes {
		state.recorder.Intent("volume", volume, "retain")
	}
	state.recorder.Intent("network", opts.StackName, "retain")
	return nil
}

func (o commandOptions) output() io.Writer {
	if o.operation != nil {
		return io.Discard
	}
	return os.Stdout
}

func (s *operationState) planned(p *plan.Plan, stack string) {
	for _, change := range p.Changes {
		action := "noop"
		switch change.Kind {
		case plan.KindCreate:
			action = "create"
		case plan.KindRecreate:
			action = "recreate"
		case plan.KindRemove:
			action = "remove"
		}
		s.recorder.Planned(strings.Join([]string{stack, change.Service}, "/"), action)
	}
}
