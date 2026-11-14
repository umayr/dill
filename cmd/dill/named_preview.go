package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
)

var finishNamedSession = func(s *coordination.Session, outcome, effects string) error {
	return s.Finish(outcome, effects)
}

// finalizeNamedPreview buffers a possible successful review before publishing
// its terminal journal entry. It never exposes rows after a cleanup failure.
func finalizeNamedPreview(out io.Writer, r preflight.Result, state *operationState, code int) error {
	c := state.coordination
	if c != nil && c.operationID != "" {
		r.OperationID = c.operationID
	}
	var candidate []byte
	if r.Outcome == "succeeded" {
		if err := r.Validate(0); err != nil {
			r.Withdraw()
			r.Outcome, code = "failed", 1
			r.Errors = append(r.Errors, preflight.FixedError("PREFLIGHT_UNAVAILABLE"))
		} else {
			var err error
			candidate, err = r.Buffer(0)
			if err != nil {
				r.Withdraw()
				r.ResourcesTruncated = true
				r.Outcome, code = "failed", 1
				r.Errors = append(r.Errors, preflight.FixedError("PREFLIGHT_LIMIT_EXCEEDED"))
			}
		}
	}
	closeErr := state.releaseEngine()
	if c != nil {
		closeErr = errors.Join(closeErr, c.closeTransport())
	}
	if closeErr != nil {
		r.Withdraw()
		r.Outcome = "unknown"
		code = 1
		r.CoordinationEffects = "none"
		if c != nil && c.namedStateAttempted {
			r.CoordinationEffects = "unknown"
		}
		r.Errors = append(r.Errors, preflight.FixedError("ENGINE_CLOSE_FAILED"))
	}
	if c != nil && c.session != nil {
		journalOutcome, journalEffects := r.Outcome, "none"
		if journalOutcome == "unknown" {
			journalEffects = "unknown"
		}
		if err := finishNamedSession(c.session, journalOutcome, journalEffects); err != nil {
			r.Withdraw()
			r.Outcome = "unknown"
			r.CoordinationEffects = "unknown"
			code = 1
			r.Errors = append(r.Errors, preflight.FixedError("COORDINATION_COMMIT_FAILED"))
		}
	}
	if c != nil {
		if err := c.close(); err != nil {
			r.Withdraw()
			r.Outcome = "unknown"
			r.CoordinationEffects = "unknown"
			code = 1
			r.Errors = append(r.Errors, preflight.FixedError("COORDINATION_RELEASE_FAILED"))
		}
	}
	if r.Outcome != "succeeded" {
		var err error
		candidate, err = r.Buffer(code)
		if err != nil {
			return protocolExit{1}
		}
	}
	if n, err := out.Write(candidate); err != nil || n != len(candidate) {
		return protocolExit{1}
	}
	if code != 0 {
		return protocolExit{code}
	}
	return nil
}

// runNamedPreview is deliberately separate from operation/1: it proves engine
// none while retaining the existing conservative metadata journal axes.
func runNamedPreview(ctx context.Context, args []string, out io.Writer) error {
	restore := silenceSecretLogs()
	defer restore()
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return protocolExit{1}
	}
	r := preflight.NewResult("op_"+hex.EncodeToString(random[:]), "")
	state := &operationState{recorder: &operation.Recorder{}}
	fail := func(code int, outcome, reason string) error {
		r.Outcome = outcome
		r.Withdraw()
		r.Errors = append(r.Errors, preflight.FixedError(reason))
		if reason == "SECRET_UNAVAILABLE" {
			r.Target = nil
			r.Stack = ""
		}
		return finalizeNamedPreview(out, r, state, code)
	}
	if namedSecretIntent(args) {
		return fail(1, "failed", "SECRET_UNAVAILABLE")
	}
	inv, reason := preflight.ParseInvocation(args, false)
	r.Operation = inv.Operation
	if reason != "" {
		return fail(2, "invalid_input", reason)
	}
	if !qualifiedNativeHost() {
		return fail(2, "invalid_input", "UNSUPPORTED_PREFLIGHT")
	}
	opts := commandOptions{ConfigFile: inv.ConfigFile, ConfigFileSet: true, StackName: inv.Stack, StackNameSet: true, Engine: inv.Engine, EngineSocket: inv.Socket, EngineSocketSet: true, Timeout: inv.Timeout, Quiet: true, ForceRecreate: inv.ForceRecreate, NoPrune: inv.NoPrune, operation: state}
	endpoint, err := localEngineSocket(opts)
	if err != nil {
		return fail(2, "invalid_input", "INVALID_TARGET")
	}
	opts.EngineSocket = endpoint
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, inv.Timeout)
	defer cancel()
	state.ctx = ctx
	if ctx.Err() != nil {
		return fail(1, "cancelled", "CANCELLED")
	}
	cfg, err := loadConfig(ctx, opts)
	if errors.Is(err, config.ErrSecretUnavailable) || hasSecretGrants(cfg) || (cfg != nil && len(cfg.Secrets) != 0) {
		return fail(1, "failed", "SECRET_UNAVAILABLE")
	}
	if err != nil {
		var invalid *config.InvalidError
		if errors.As(err, &invalid) {
			return fail(2, "invalid_input", "CONFIG_INVALID")
		}
		if ctx.Err() != nil {
			return fail(1, "cancelled", "CANCELLED")
		}
		return fail(1, "failed", "EVALUATION_FAILED")
	}
	if err = config.ValidateRuntime(cfg); err != nil {
		return fail(2, "invalid_input", "CONFIG_INVALID")
	}
	if namedDeclaredLimitExceeded(cfg, inv.Stack) {
		r.ResourcesTruncated = true
		return fail(1, "failed", "PREFLIGHT_LIMIT_EXCEEDED")
	}
	// Use an effective copy for the same supported never-pull profile as prepare,
	// without replacing any desired image reference or service hash provenance.
	effective := *cfg
	effective.Services = make(map[string]*config.Service, len(cfg.Services))
	for name, svc := range cfg.Services {
		if svc == nil {
			return fail(2, "invalid_input", "CONFIG_INVALID")
		}
		copy := *svc
		if inv.Operation != "up" || inv.Pull == "never" {
			copy.PullPolicy = "never"
		}
		effective.Services[name] = &copy
	}
	if err = orchestrator.ValidateDisposableConfig(&effective); err != nil {
		return fail(2, "invalid_input", "CONFIG_INVALID")
	}
	state.config = cfg
	c := &operationCoordination{options: coordinationOptions{directory: inv.StateDir, layout: inv.Layout, daemonID: inv.DaemonID, source: inv.Source}, collectNamed: true, namedReadOnly: true}
	state.coordination = c
	err = c.prepare(ctx, opts, cfg, inv.Operation, inv.Pull)
	if c.target != nil {
		r.Target = &preflight.Target{Engine: "docker", EndpointDigest: operation.Digest(inv.Socket), QualifiedIdentityDigest: "sha256:" + c.target.Identity()}
		r.Stack = inv.Stack
	}
	if c.namedStateAttempted {
		r.CoordinationEffects = "unknown"
	}
	if c.session != nil {
		r.CoordinationEffects = "written"
	}
	if err != nil {
		if errors.Is(err, config.ErrSecretUnavailable) {
			return fail(1, "failed", "SECRET_UNAVAILABLE")
		}
		if ctx.Err() != nil {
			return fail(1, "cancelled", "CANCELLED")
		}
		if c.target == nil {
			var invalid usageError
			if errors.As(err, &invalid) {
				return fail(2, "invalid_input", "CONFIG_INVALID")
			}
			return fail(1, "failed", "QUALIFICATION_FAILED")
		}
		if c.session == nil {
			return fail(1, "unknown", "COORDINATION_UNAVAILABLE")
		}
		return fail(1, "failed", "PREFLIGHT_UNAVAILABLE")
	}
	scope, err := c.state.NamedPreflightScope()
	if err != nil {
		return fail(1, "failed", "PREFLIGHT_UNAVAILABLE")
	}
	_, selection, err := produceNamedCapture(ctx, inv, &effective, c.namedInventory, *r.Target, scope)
	if errors.Is(err, config.ErrSecretUnavailable) {
		return fail(1, "failed", "SECRET_UNAVAILABLE")
	}
	if err != nil {
		return fail(1, "failed", "PREFLIGHT_UNAVAILABLE")
	}
	selectionDigest, err := selection.Digest()
	if err != nil {
		return fail(1, "failed", "PREFLIGHT_UNAVAILABLE")
	}
	r.InputDigest = &selection.InputDigest
	r.SelectionDigest = &selectionDigest
	r.Resources = selection.Resources
	r.Coverage = selection.Coverage
	r.Outcome = "succeeded"
	r.CoordinationEffects = "written"
	return finalizeNamedPreview(out, r, state, 0)
}
