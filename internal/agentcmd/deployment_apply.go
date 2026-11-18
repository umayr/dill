package agentcmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func validApplyID(id string) bool {
	v, e := hex.DecodeString(id)
	return e == nil && len(v) == 16 && hex.EncodeToString(v) == id
}
func validDeployStack(name string) bool { return preflight.ValidName(name) && len(name) <= 128 }

func validDeploymentStatus(r deploymentStatus) bool {
	if r.OperationID != "" && (!strings.HasPrefix(r.OperationID, "op_") || !validApplyID(strings.TrimPrefix(r.OperationID, "op_"))) {
		return false
	}
	switch r.State {
	case "pending":
		return r.Effects == "unknown" && r.OperationID == ""
	case "unknown":
		return r.Effects == "unknown" && r.OperationID == ""
	case "succeeded":
		return r.Effects == "complete" && r.OperationID != ""
	case "failed":
		return r.Effects == "none" || (r.Effects == "partial" && r.OperationID != "")
	}
	return false
}

type applyEndpoint struct {
	previewEndpoint
	history *deploymentHistory
	apply   func(context.Context, string, string, string, string, string) deploymentStatus
	slot    chan struct{}
}

func (h *applyEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/deploy/apply" {
		id := strings.TrimPrefix(r.URL.Path, "/v1/deploy/operations/")
		if !validApplyID(id) || r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.NotFound(w, r)
			return
		}
		status, ok := h.history.status(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeDeploymentStatus(w, status)
		return
	}
	fields, ok := deploymentFields(w, r, "stageId", "manifestSha256", "stack", "selectionDigest", "requestId", "confirm")
	if !ok || fields["confirm"] != "create" || !validApplyID(fields["stageId"]) || !preflight.ValidDigest("sha256:"+fields["manifestSha256"]) ||
		!validDeployStack(fields["stack"]) || !preflight.ValidDigest(fields["selectionDigest"]) || !validApplyID(fields["requestId"]) {
		http.Error(w, "invalid deployment confirmation", http.StatusBadRequest)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "deployment busy", http.StatusConflict)
		return
	}
	id, stack := fields["requestId"], fields["stack"]
	if !h.history.canReserve(id, stack) {
		http.Error(w, "creation already recorded or blocked; check operation status", http.StatusConflict)
		return
	}
	// Accept no operation after disconnect until durable admission. Once
	// admitted, the bounded child and its record are independent of delivery.
	verifyCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var reserved bool
	var qualifiedTarget string
	err := h.stages.WithVerifiedEntrypoint(verifyCtx, fields["stageId"], fields["manifestSha256"], func(path string) error {
		preview, err := h.preview(verifyCtx, path, stack)
		if err != nil || preview.SelectionDigest == nil || *preview.SelectionDigest != fields["selectionDigest"] || preview.Target == nil || len(preview.Resources) == 0 || verifyCtx.Err() != nil {
			return errOperationConflict
		}
		for _, resource := range preview.Resources {
			if resource.Presence != "absent" {
				return errOperationConflict // Not an adoption path.
			}
		}
		qualifiedTarget = preview.Target.QualifiedIdentityDigest
		return nil
	})
	if err == nil {
		// Preflight evaluates trusted Pkl. Revalidate the staged bytes again
		// before reserving and launching the checked operation.
		err = h.stages.WithVerifiedEntrypoint(verifyCtx, fields["stageId"], fields["manifestSha256"], func(path string) error {
			if err := verifyCtx.Err(); err != nil {
				return err
			}
			if err := h.history.reserve(id, stack); err != nil {
				return err
			}
			reserved = true
			execCtx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
			defer stop()
			return h.history.finish(h.apply(execCtx, path, stack, fields["selectionDigest"], id, qualifiedTarget))
		})
	}
	if err != nil && !reserved {
		http.Error(w, "creation not admitted; review the target and check operation status", http.StatusConflict)
		return
	}
	status, ok := h.history.status(id)
	if !ok {
		http.Error(w, "deployment outcome unavailable", http.StatusServiceUnavailable)
		return
	}
	writeDeploymentStatus(w, status)
}

func writeDeploymentStatus(w http.ResponseWriter, status deploymentStatus) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func cliApply(exe, pkl, socket, daemon, state string) func(context.Context, string, string, string, string, string) deploymentStatus {
	return func(ctx context.Context, path, stack, selection, id, qualifiedTarget string) deploymentStatus {
		unknown := deploymentStatus{RequestID: id, Stack: stack, State: "unknown", Effects: "unknown"}
		cmd := exec.CommandContext(ctx, exe, "operation", "up", "--contract", operation.Version, "-f", path, "-p", stack,
			"--engine", "docker", "--engine-socket", socket, "--coordination-layout", coordination.NativeDockerLayout,
			"--coordination-daemon-id", daemon, "--coordination-state-dir", state, "--coordination-source", "brine",
			"--no-prune", "--pull", "never", "--timeout", "4m30s", "--preflight-contract", preflight.Version, "--expect-preflight", selection)
		cmd.Dir, cmd.Env, cmd.WaitDelay = filepath.Dir(path), []string{"DILL_PKL_PATH=" + pkl}, 2*time.Second
		var out limitedPreviewOutput
		cmd.Stdout, cmd.Stderr = &out, io.Discard
		err := cmd.Run()
		if ctx.Err() != nil {
			return unknown
		}
		var exit *exec.ExitError
		if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
			return unknown
		}
		var result operation.Result
		dec := json.NewDecoder(&out.buf)
		dec.DisallowUnknownFields()
		if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || result.Version != operation.Version || result.Operation != "up" || result.Stack != stack || result.Target == nil ||
			result.Target.Engine != "docker" || result.Target.EndpointDigest != operation.Digest(socket) || result.Target.QualifiedIdentityDigest != qualifiedTarget ||
			result.Resources == nil || result.Errors == nil || result.ResourcesTruncated || !strings.HasPrefix(result.OperationID, "op_") || !validApplyID(strings.TrimPrefix(result.OperationID, "op_")) {
			return unknown
		}
		done := deploymentStatus{RequestID: id, Stack: stack, OperationID: result.OperationID, Effects: result.Effects}
		switch {
		case err == nil && result.Outcome == "succeeded" && result.Effects == "complete" && len(result.Errors) == 0 && len(result.Resources) > 0:
			done.State = "succeeded"
		case err != nil && (result.Outcome == "failed" || result.Outcome == "cancelled") && (result.Effects == "none" || result.Effects == "partial") && len(result.Errors) > 0:
			done.State = "failed"
		default:
			return unknown
		}
		if !validDeploymentStatus(done) {
			return unknown
		}
		return done
	}
}
