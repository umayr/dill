package agentcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/preflight"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type brokerCLIReply struct {
	Contract  string                  `json:"contract"`
	Reference *brokersecret.Reference `json:"reference,omitempty"`
	Prepared  *brokersecret.Review    `json:"prepared,omitempty"`
	Preview   *preflight.Result       `json:"preview,omitempty"`
	Plan      *plan.Plan              `json:"plan,omitempty"`
	Operation *operation.Result       `json:"operation,omitempty"`
	Code      string                  `json:"code,omitempty"`
}
type brokerCLI struct{ exe, pkl, socket, daemon, state, store, profile, engine string }

func (c brokerCLI) args(kind, path string, s brokersecret.Scope) []string {
	args := []string{kind, "--contract", brokersecret.Version, "--broker-store", c.store, "-f", path, "-p", s.Stack, "--coordination-state-dir", c.state, "--source-digest", s.SourceDigest}
	if c.profile != "" {
		return append(args, "--broker-profile", c.profile)
	}
	return append(args, "--engine-socket", c.socket, "--coordination-daemon-id", c.daemon)
}
func (c brokerCLI) call(ctx context.Context, kind, path string, s brokersecret.Scope, in io.Reader) (brokerCLIReply, error) {
	var reply brokerCLIReply
	// A namespace-UID0 process must not fall back to the initial host root home
	// during SDK/config initialization. Give each child a private empty home,
	// never the operator/source environment or ambient container-engine config.
	home, err := os.MkdirTemp("", "dill-broker-home-")
	if err != nil {
		return reply, brokersecret.ErrUnavailable
	}
	defer os.RemoveAll(home)
	cmd := exec.CommandContext(ctx, c.exe, c.args(kind, path, s)...)
	cmd.Dir = filepath.Dir(path)
	cmd.Env = []string{"DILL_PKL_PATH=" + c.pkl, "PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "XDG_CACHE_HOME=" + filepath.Join(home, "cache")}
	cmd.WaitDelay = 2 * time.Second
	var out limitedPreviewOutput
	cmd.Stdin = in
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err = cmd.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return reply, brokersecret.ErrUnavailable
	}
	if brokersecret.DecodeMetadata(&out.buf, &reply) != nil || reply.Contract != brokersecret.Version || reply.Code != "" {
		return brokerCLIReply{}, brokersecret.ErrUnavailable
	}
	return reply, err
}
func (c brokerCLI) prepare(ctx context.Context, path string, e brokersecret.Envelope) (brokersecret.Review, error) {
	raw, err := brokersecret.Encode(e)
	if err != nil {
		return brokersecret.Review{}, err
	}
	defer clear(raw)
	reply, err := c.call(ctx, "broker-prepare", path, e.Scope, bytes.NewReader(raw))
	if err != nil || reply.Prepared == nil || reply.Prepared.Scope != e.Scope || reply.Prepared.Active || !brokersecret.ValidHandle(reply.Prepared.Handle) || !reply.Prepared.ExpiresAt.After(time.Now()) || reply.Prepared.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		return brokersecret.Review{}, brokersecret.ErrUnavailable
	}
	return *reply.Prepared, nil
}
func (c brokerCLI) review(ctx context.Context, path string, ref brokersecret.Reference) (brokerCLIReply, error) {
	raw, err := json.Marshal(ref)
	if err != nil {
		return brokerCLIReply{}, brokersecret.ErrInvalid
	}
	reply, err := c.call(ctx, "broker-preflight", path, ref.Scope, bytes.NewReader(raw))
	if err != nil || reply.Reference == nil || *reply.Reference != ref || reply.Preview == nil || reply.Plan == nil {
		return brokerCLIReply{}, brokersecret.ErrUnavailable
	}
	p := reply.Preview
	if p.ValidateBroker(0) != nil || p.Operation != "up" || p.Stack != ref.Scope.Stack || p.SelectionDigest == nil || p.Target == nil || p.Target.Engine != ref.Scope.Engine || p.Target.EndpointDigest != ref.Scope.EndpointDigest || p.Target.QualifiedIdentityDigest != ref.Scope.TargetDigest {
		return brokerCLIReply{}, brokersecret.ErrUnavailable
	}
	return reply, nil
}
func (c brokerCLI) apply(ctx context.Context, path string, ref brokersecret.Reference, digest, id string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
	unknown := deploymentStatus{RequestID: id, Stack: ref.Scope.Stack, State: "unknown", Effects: "unknown"}
	input := struct {
		brokersecret.Reference
		SelectionDigest string                       `json:"selectionDigest"`
		Ownership       operation.ManagedExpectation `json:"ownership"`
	}{ref, digest, expected}
	raw, err := json.Marshal(input)
	if err != nil {
		return unknown, nil
	}
	reply, err := c.call(ctx, "broker-managed-operation", path, ref.Scope, bytes.NewReader(raw))
	if reply.Reference == nil || *reply.Reference != ref || reply.Operation == nil {
		return unknown, nil
	}
	r := reply.Operation
	if r.Version != operation.Version || r.Operation != "up" || r.Stack != ref.Scope.Stack || r.Target == nil || r.Target.Engine != ref.Scope.Engine || r.Target.EndpointDigest != ref.Scope.EndpointDigest || r.Target.QualifiedIdentityDigest != ref.Scope.TargetDigest || r.Resources == nil || r.Errors == nil || r.ResourcesTruncated || !strings.HasPrefix(r.OperationID, "op_") || !validApplyID(strings.TrimPrefix(r.OperationID, "op_")) {
		return unknown, nil
	}
	done := deploymentStatus{RequestID: id, Stack: ref.Scope.Stack, OperationID: r.OperationID, Effects: r.Effects}
	if err == nil && r.Outcome == "succeeded" && (r.Effects == "complete" || r.Effects == "none") && len(r.Errors) == 0 && r.ManagedResources != nil && (operation.ManagedExpectation{Mode: "update", Resources: *r.ManagedResources}).Valid() {
		done.State = "succeeded"
		return done, *r.ManagedResources
	}
	if err != nil && (r.Outcome == "failed" || r.Outcome == "cancelled") && (r.Effects == "none" || r.Effects == "partial") && len(r.Errors) > 0 && r.ManagedResources == nil {
		done.State = "failed"
		return done, nil
	}
	return unknown, nil
}
