package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"slices"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
)

var errNamedMismatch = errors.New("reviewed selection changed")

// bindNamedOperation computes the fresh selection under the retained session,
// then installs only restrictions and the exact in-process handler capture.
// No engine mutation or ensure occurs before the successful comparison.
func bindNamedOperation(ctx context.Context, opts commandOptions, cfg *config.DillConfig, inv preflight.Invocation) error {
	state := opts.operation
	if state == nil || state.coordination == nil || state.engine == nil {
		return errNamedCaptureUnavailable
	}
	c := state.coordination
	if c.target == nil || c.state == nil || c.session == nil || c.namedInventory == nil {
		return errNamedCaptureUnavailable
	}
	effective := *cfg
	effective.Services = make(map[string]*config.Service, len(cfg.Services))
	for name, svc := range cfg.Services {
		if svc == nil {
			return errNamedCaptureUnavailable
		}
		copy := *svc
		if inv.Operation != "up" || inv.Pull == "never" {
			copy.PullPolicy = "never"
		}
		effective.Services[name] = &copy
	}
	scope, err := c.state.NamedPreflightScope()
	if err != nil {
		return errNamedCaptureUnavailable
	}
	target := preflight.Target{Engine: "docker", EndpointDigest: operation.Digest(inv.Socket), QualifiedIdentityDigest: "sha256:" + c.target.Identity()}
	capture, selection, err := produceNamedCapture(ctx, inv, &effective, c.namedInventory, target, scope)
	if errors.Is(err, config.ErrSecretUnavailable) {
		return err
	}
	if err != nil {
		return errNamedCaptureUnavailable
	}
	digest, err := selection.Digest()
	if err != nil {
		return errNamedCaptureUnavailable
	}
	if subtle.ConstantTimeCompare([]byte(digest), []byte(inv.ExpectedPreflight)) != 1 {
		return errNamedMismatch
	}
	enforcer, ok := any(state.engine).(orchestrator.PreparedImageEnforcer)
	if !ok {
		return errNamedCaptureUnavailable
	}
	images := make(map[string]orchestrator.PreparedImage, len(cfg.Services))
	for name, svc := range cfg.Services {
		id, ok := c.namedInventory.prepared[name]
		if !ok || svc == nil {
			return errNamedCaptureUnavailable
		}
		images[name] = orchestrator.PreparedImage{Reference: svc.Image, ID: id}
	}
	if err = enforcer.EnforcePreparedImages(images); err != nil {
		return errNamedCaptureUnavailable
	}
	capture.compared = true
	state.named = capture
	state.listed = slices.Clone(c.namedInventory.listed)
	state.census = make(map[string]string, len(c.namedInventory.containers))
	for name, live := range c.namedInventory.containers {
		if live != nil {
			state.census[name] = live.ID
		} else {
			state.census[name] = ""
		}
	}
	return nil
}
