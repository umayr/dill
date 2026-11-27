package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/preflight"
	"io"
	"path/filepath"
	"time"
)

// Broker commands are explicit administrator/agent boundaries. Paths and daemon
// identity come from trusted operator configuration, never an HTTP payload.
// Values enter only Prepare's bounded stdin envelope, never argv or output.
type brokerCLIOptions struct {
	file, stack, socket, daemon, state, store, source, profilePath string
	profile                                                        *coordination.BrokerProfile
}

func parseBrokerCLI(kind string, args []string) (brokerCLIOptions, error) {
	var o brokerCLIOptions
	fields := map[string]*string{"contract": new(string), "broker-store": &o.store}
	if kind != "broker-init" {
		fields["broker-profile"] = &o.profilePath
		fields["f"] = &o.file
		fields["p"] = &o.stack
		fields["engine-socket"] = &o.socket
		fields["coordination-daemon-id"] = &o.daemon
		fields["coordination-state-dir"] = &o.state
		fields["source-digest"] = &o.source
	}
	switch kind {
	case "broker-init", "broker-prepare", "broker-retirement-prepare", "broker-preflight", "broker-managed-operation":
	default:
		return o, brokersecret.ErrInvalid
	}
	fs := flag.NewFlagSet(kind, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	seen := map[string]bool{}
	for name, dst := range fields {
		fs.Func(name, "", func(v string) error {
			if seen[name] || v == "" {
				return brokersecret.ErrInvalid
			}
			seen[name] = true
			*dst = v
			return nil
		})
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || *fields["contract"] != brokersecret.Version {
		return o, brokersecret.ErrInvalid
	}
	for name := range fields {
		if kind != "broker-init" && (name == "broker-profile" || name == "engine-socket" || name == "coordination-daemon-id") {
			continue
		}
		if !seen[name] {
			return o, brokersecret.ErrInvalid
		}
	}
	if kind != "broker-init" {
		if seen["broker-profile"] {
			if seen["engine-socket"] || seen["coordination-daemon-id"] {
				return o, brokersecret.ErrInvalid
			}
		} else if !seen["engine-socket"] || !seen["coordination-daemon-id"] {
			return o, brokersecret.ErrInvalid
		}
	}
	for _, p := range []string{o.store} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return o, brokersecret.ErrInvalid
		}
	}
	if kind != "broker-init" {
		paths := []string{o.file, o.state}
		if o.profilePath != "" {
			paths = append(paths, o.profilePath)
		} else {
			paths = append(paths, o.socket)
		}
		for _, p := range paths {
			if !filepath.IsAbs(p) || filepath.Clean(p) != p {
				return o, brokersecret.ErrInvalid
			}
		}
		if config.ValidateSecretStackName(o.stack) != nil || !preflight.ValidDigest(o.source) {
			return o, brokersecret.ErrInvalid
		}
		if _, err := coordination.DockerIdentity(o.daemon); o.profilePath == "" && err != nil {
			return o, brokersecret.ErrInvalid
		}
	}
	return o, nil
}

type brokerCLIRequest struct {
	brokersecret.Reference
	SelectionDigest string                        `json:"selectionDigest,omitempty"`
	Ownership       *operation.ManagedExpectation `json:"ownership,omitempty"`
}
type brokerCLIReply struct {
	Contract  string                  `json:"contract"`
	Reference *brokersecret.Reference `json:"reference,omitempty"`
	Prepared  *brokersecret.Review    `json:"prepared,omitempty"`
	Preview   *preflight.Result       `json:"preview,omitempty"`
	Plan      *plan.Plan              `json:"plan,omitempty"`
	Operation *operation.Result       `json:"operation,omitempty"`
	Code      string                  `json:"code,omitempty"`
}

func (o brokerCLIOptions) nativeArgs(mutate bool, selection string) []string {
	contract := preflight.Version
	if mutate {
		contract = operation.Version
	}
	engine, layout := "docker", coordination.NativeDockerLayout
	if o.profile != nil {
		engine = o.profile.Engine()
		if o.profile.Docker != nil {
			layout = o.profile.Docker.Layout
		} else {
			layout = o.profile.Podman.Layout
		}
	}
	args := []string{"up", "--contract", contract, "-f", o.file, "-p", o.stack, "--engine", engine, "--engine-socket", o.socket, "--coordination-layout", layout, "--coordination-state-dir", o.state, "--coordination-source", "brine", "--timeout", "4m30s", "--no-prune", "--pull", "never"}
	if o.daemon != "" {
		args = append(args, "--coordination-daemon-id", o.daemon)
	}
	if mutate {
		args = append(args, "--preflight-contract", preflight.Version, "--expect-preflight", selection)
	}
	return args
}
func (o brokerCLIOptions) validateScope(s brokersecret.Scope) bool {
	if o.profile != nil {
		digest, err := o.profile.TargetDigest()
		return err == nil && s.Valid() && s.Stack == o.stack && s.Engine == o.profile.Engine() && s.SourceDigest == o.source && s.TargetDigest == digest && s.EndpointDigest == brokersecret.Digest([]byte("unix://"+o.socket))
	}
	id, err := coordination.DockerIdentity(o.daemon)
	return err == nil && s.Valid() && s.Stack == o.stack && s.Engine == "docker" && s.SourceDigest == o.source && s.TargetDigest == "sha256:"+id && s.EndpointDigest == brokersecret.Digest([]byte("unix://"+o.socket))
}
func brokerCLIConfig(ctx context.Context, o brokerCLIOptions, s brokersecret.Scope) (*config.DillConfig, brokersecret.Requirements, error) {
	scoped, err := brokerInputContext(ctx, o.file)
	if err != nil {
		return nil, brokersecret.Requirements{}, err
	}
	cfg, err := configLoader(scoped, o.file)
	if err != nil {
		return nil, brokersecret.Requirements{}, brokersecret.ErrUnavailable
	}
	req, err := brokersecret.Describe(cfg, o.stack)
	if err != nil || !o.validateScope(s) || req.ConfigDigest != s.ConfigDigest || req.Engine != s.Engine {
		return nil, req, brokersecret.ErrUnavailable
	}
	return cfg, req, nil
}

type brokerOutput struct{ bytes.Buffer }

func (b *brokerOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > brokersecret.MaxWire {
		return 0, brokersecret.ErrCapacity
	}
	return b.Buffer.Write(p)
}
func executeBrokerCLI(ctx context.Context, kind string, o brokerCLIOptions, in io.Reader) (reply brokerCLIReply, retErr error) {
	reply.Contract = brokersecret.Version
	if kind == "broker-init" {
		return reply, brokersecret.Provision(o.store)
	}
	// Parse and validate all untrusted input before accessing protected state.
	var request brokerCLIRequest
	var envelope brokersecret.Envelope
	var scope brokersecret.Scope
	var retirement brokersecret.Retirement
	if kind == "broker-retirement-prepare" {
		var err error
		retirement, err = brokersecret.DecodeRetirement(in)
		if err != nil {
			return reply, err
		}
		scope = retirement.Scope
	} else if kind == "broker-prepare" {
		var err error
		envelope, err = brokersecret.Decode(in)
		if err != nil {
			return reply, err
		}
		defer envelope.Close()
		scope = envelope.Scope
	} else {
		var decodeErr error
		if kind == "broker-preflight" {
			decodeErr = brokersecret.DecodeMetadata(in, &request.Reference)
		} else {
			decodeErr = brokersecret.DecodeMetadata(in, &request)
		}
		if decodeErr != nil || !request.Reference.Valid() {
			return reply, brokersecret.ErrInvalid
		}
		if kind == "broker-preflight" && (request.Ownership != nil || request.SelectionDigest != "") {
			return reply, brokersecret.ErrInvalid
		}
		if kind == "broker-managed-operation" && (request.Ownership == nil || !request.Ownership.Valid() || (request.Ownership.Mode != "create" && request.Ownership.Mode != "update") || !preflight.ValidDigest(request.SelectionDigest)) {
			return reply, brokersecret.ErrInvalid
		}
		scope = request.Scope
	}
	if o.profilePath != "" {
		p, err := coordination.ReadBrokerProfile(o.profilePath)
		if err != nil {
			return reply, brokersecret.ErrUnavailable
		}
		o.profile = &p
		o.socket = p.Socket()
		if p.Docker != nil {
			o.daemon = p.Docker.DaemonID
		}
	}
	if !o.validateScope(scope) {
		return reply, brokersecret.ErrInvalid
	}
	store, err := brokersecret.Open(o.store)
	if err != nil {
		return reply, err
	}
	defer func() {
		if store.Close() != nil {
			reply = brokerCLIReply{Contract: brokersecret.Version}
			retErr = brokersecret.ErrUnavailable
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute+30*time.Second)
	defer cancel()
	cfg, req, err := brokerCLIConfig(ctx, o, scope)
	if err != nil {
		return reply, err
	}
	if kind == "broker-retirement-prepare" && (len(req.Names) != 0 || len(req.Grants) != 0) {
		return reply, brokersecret.ErrInvalid
	}
	if kind == "broker-prepare" || kind == "broker-retirement-prepare" {
		var target *coordination.Target
		var err error
		if o.profile != nil {
			target, _, err = o.profile.Qualify(ctx)
		} else {
			target, err = coordination.Qualify(ctx, coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: o.socket, DaemonID: o.daemon})
		}
		if err != nil {
			return reply, brokersecret.ErrUnavailable
		}
		matches := (target.BrokerRootful() || o.profile != nil) && scope.TargetDigest == "sha256:"+target.Identity() && scope.EndpointDigest == target.EndpointDigest()
		if o.profile != nil && target.NamespaceDigest() != "" {
			if target.CheckRuntime(o.store, nil) != nil || !brokerProfileOwners(o.profile, req.Grants) {
				matches = false
			}
		}
		closeErr := target.Close()
		if !matches || closeErr != nil {
			return reply, brokersecret.ErrUnavailable
		}
		var review brokersecret.Review
		if kind == "broker-retirement-prepare" {
			review, err = store.PrepareRetirement(retirement)
		} else {
			review, err = store.Prepare(envelope, scope, req.Grants)
		}
		if err != nil {
			return reply, err
		}
		reply.Prepared = &review
		return reply, nil
	}
	material, err := store.Resolve(request.Handle, scope)
	if err != nil {
		return reply, err
	}
	if _, _, err = material.References(cfg, false); err != nil {
		return reply, err
	}
	reply.Reference = &request.Reference
	var output brokerOutput
	if kind == "broker-preflight" {
		var p *plan.Plan
		retErr = runNamedPreviewScoped(ctx, o.nativeArgs(false, ""), &output, material, cfg, &p, o.nativeProfiles()...)
		var result preflight.Result
		if json.Unmarshal(output.Bytes(), &result) != nil {
			return brokerCLIReply{Contract: brokersecret.Version}, brokersecret.ErrUnavailable
		}
		reply.Preview = &result
		if retErr == nil && result.Outcome == "succeeded" {
			reply.Plan = p
		}
	} else {
		retErr = runOperationScoped(ctx, o.nativeArgs(true, request.SelectionDigest), request.Ownership, material, cfg, &output, o.nativeProfiles()...)
		var result operation.Result
		if json.Unmarshal(output.Bytes(), &result) != nil {
			return brokerCLIReply{Contract: brokersecret.Version}, brokersecret.ErrUnavailable
		}
		reply.Operation = &result
	}
	return reply, retErr
}
func runBrokerCLI(ctx context.Context, kind string, args []string, in io.Reader, out io.Writer) error {
	restore := silenceSecretLogs()
	defer restore()
	o, err := parseBrokerCLI(kind, args)
	var reply brokerCLIReply
	if err == nil {
		reply, err = executeBrokerCLI(ctx, kind, o, in)
	}
	code := 0
	if err != nil {
		code = 1
		if reply.Contract == "" {
			reply.Contract = brokersecret.Version
		}
		if reply.Preview == nil && reply.Operation == nil {
			reply.Code = "BROKER_UNAVAILABLE"
			if errors.Is(err, brokersecret.ErrInvalid) {
				reply.Code = "INVALID_INPUT"
				code = 2
			}
			if errors.Is(err, brokersecret.ErrUnsupported) {
				reply.Code = "BROKER_UNSUPPORTED"
			}
		}
	}
	if json.NewEncoder(out).Encode(reply) != nil {
		return protocolExit{1}
	}
	if code != 0 {
		return protocolExit{code}
	}
	return nil
}
