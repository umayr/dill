package agentcmd

import (
	"bytes"
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

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

func validApplyID(id string) bool {
	v, e := hex.DecodeString(id)
	return e == nil && len(v) == 16 && hex.EncodeToString(v) == id
}
func validDeployStack(name string) bool { return preflight.ValidName(name) && len(name) <= 128 }
func validDeploymentStatus(r deploymentStatus) bool {
	if r.Action != "" && !validLifecycleAction(r.Action) {
		return false
	}
	if r.OperationID != "" && (!strings.HasPrefix(r.OperationID, "op_") || !validApplyID(strings.TrimPrefix(r.OperationID, "op_"))) {
		return false
	}
	switch r.State {
	case "pending", "unknown":
		return r.Effects == "unknown" && r.OperationID == ""
	case "succeeded":
		return (r.Effects == "complete" || (validLifecycleAction(r.Action) && r.Effects == "none")) && r.OperationID != ""
	case "failed":
		return r.Effects == "none" || (r.Effects == "partial" && r.OperationID != "")
	}
	return false
}

type applyEndpoint struct {
	previewEndpoint
	history          *deploymentHistory
	down             func(context.Context, string, string) (preflight.Result, error)
	apply            func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource)
	slot             chan struct{}
	lifecyclePreview map[string]func(context.Context, string, string) (preflight.Result, error)
	lifecycleApply   map[string]func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource)
}

func ownership(mode string, previous deploymentRecord) operation.ManagedExpectation {
	resources := previous.Owned
	if mode == "create" {
		resources = []operation.ManagedResource{}
	}
	return operation.ManagedExpectation{Mode: mode, Resources: resources}
}

func (h *applyEndpoint) review(w http.ResponseWriter, r *http.Request) {
	remove := r.URL.Path == "/v1/deploy/remove-preview"
	var fields map[string]string
	var ok bool
	if remove {
		fields, ok = deploymentFields(w, r, "stack")
	} else {
		fields, ok = deploymentFields(w, r, "stack", "stageId", "manifestSha256")
		ok = ok && validApplyID(fields["stageId"]) && preflight.ValidDigest("sha256:"+fields["manifestSha256"])
	}
	stack := fields["stack"]
	if !ok || !validDeployStack(stack) {
		http.Error(w, "invalid preview request", 400)
		return
	}
	previous, mode := h.history.managed(stack)
	stage, manifest, preview := fields["stageId"], fields["manifestSha256"], h.preview
	if remove {
		if mode != "update" || h.down == nil {
			http.Error(w, "managed removal preview unavailable", 409)
			return
		}
		mode, stage, manifest, preview = "remove", previous.StageID, previous.Manifest, h.down
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result preflight.Result
	err := h.stages.WithVerifiedEntrypoint(ctx, stage, manifest, func(path string) error {
		var err error
		result, err = preview(ctx, path, stack)
		return err
	})
	if err != nil || ctx.Err() != nil {
		http.Error(w, "deployment preview unavailable", 409)
		return
	}
	if ownership(mode, previous).Matches(result.Resources) {
		w.Header().Set("X-Dill-Deployment-Mode", mode)
		if mode == "create" {
			w.Header().Set("X-Dill-Create-Enabled", "1")
		}
		if remove {
			w.Header().Set("X-Dill-Prior-Request", previous.RequestID)
		}
	} else if remove {
		http.Error(w, "managed ownership no longer matches", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

const (
	journalHeader         = "X-Dill-Journal-Id"
	journalCapacityHeader = "X-Dill-Journal-Capacity"
)

func (h *applyEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	journal := r.Header.Values(journalHeader)
	fence := r.URL.Path == "/v1/deploy/fence"
	bound := fence || r.URL.Path == "/v1/deploy/journal-apply" || lifecyclePath(r.URL.Path) != ""
	if (bound || len(journal) != 0) && (len(journal) != 1 || !validApplyID(journal[0]) || journal[0] != h.history.journal) {
		http.Error(w, "deployment journal does not match", http.StatusConflict)
		return
	}
	w.Header().Set(journalHeader, h.history.journal)
	if h.history.nearCapacity() {
		w.Header().Set(journalCapacityHeader, "low")
	}
	if action := lifecyclePath(r.URL.Path); action != "" {
		h.serveLifecycle(w, r, action)
		return
	}
	if fence {
		fields, ok := deploymentFields(w, r, "requestId", "stack", "confirm")
		if !ok || fields["confirm"] != "fence-if-unadmitted" || !validApplyID(fields["requestId"]) || !validDeployStack(fields["stack"]) {
			http.Error(w, "invalid fence confirmation", http.StatusBadRequest)
			return
		}
		if r.Context().Err() != nil {
			http.Error(w, "request cancelled", http.StatusRequestTimeout)
			return
		}
		status, err := h.history.fence(fields["requestId"], fields["stack"])
		if errors.Is(err, errHistoryCapacity) {
			w.Header().Set(journalCapacityHeader, "low")
			http.Error(w, errHistoryCapacity.Error(), http.StatusInsufficientStorage)
			return
		}
		if err != nil {
			http.Error(w, "request could not be fenced; inspect the deployment journal", http.StatusConflict)
			return
		}
		writeDeploymentStatus(w, status)
		return
	}
	switch r.URL.Path {
	case "/v1/deploy/preview", "/v1/deploy/remove-preview":
		h.review(w, r)
		return
	case "/v1/deploy/apply", "/v1/deploy/journal-apply", "/v1/deploy/remove":
	default:
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
	remove := r.URL.Path == "/v1/deploy/remove"
	var fields map[string]string
	var ok bool
	if remove {
		fields, ok = deploymentFields(w, r, "stack", "selectionDigest", "requestId", "confirm", "priorRequestId")
		ok = ok && fields["confirm"] == "remove" && validApplyID(fields["priorRequestId"])
	} else {
		fields, ok = deploymentFields(w, r, "stageId", "manifestSha256", "stack", "selectionDigest", "requestId", "confirm")
		ok = ok && (fields["confirm"] == "create" || fields["confirm"] == "update") && validApplyID(fields["stageId"]) && preflight.ValidDigest("sha256:"+fields["manifestSha256"])
	}
	if !ok || !validDeployStack(fields["stack"]) || !preflight.ValidDigest(fields["selectionDigest"]) || !validApplyID(fields["requestId"]) {
		http.Error(w, "invalid deployment confirmation", 400)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "deployment busy", 409)
		return
	}
	id, stack := fields["requestId"], fields["stack"]
	if _, exists := h.history.status(id); exists {
		http.Error(w, "request already recorded; check operation status", http.StatusConflict)
		return
	}
	previous, mode := h.history.managed(stack)
	stage, manifest, preview := fields["stageId"], fields["manifestSha256"], h.preview
	if remove && mode == "update" && previous.RequestID == fields["priorRequestId"] {
		mode, stage, manifest, preview = "remove", previous.StageID, previous.Manifest, h.down
	}
	if preview == nil || mode != fields["confirm"] {
		http.Error(w, "deployment not eligible; check operation status", 409)
		return
	}
	expected := ownership(mode, previous)
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: stack, State: "pending", Effects: "unknown"}, Mode: mode, StageID: stage, Manifest: manifest}
	// Revalidate before durable admission and detach only after admission.
	verifyCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var reserved bool
	var qualifiedTarget string
	err := h.stages.WithVerifiedEntrypoint(verifyCtx, stage, manifest, func(path string) error {
		plan, err := preview(verifyCtx, path, stack)
		if err != nil || plan.SelectionDigest == nil || *plan.SelectionDigest != fields["selectionDigest"] || plan.Target == nil || !expected.Matches(plan.Resources) || verifyCtx.Err() != nil {
			return errOperationConflict
		}
		qualifiedTarget = plan.Target.QualifiedIdentityDigest
		return nil
	})
	if err == nil {
		err = h.stages.WithVerifiedEntrypoint(verifyCtx, stage, manifest, func(path string) error {
			if err := verifyCtx.Err(); err != nil {
				return err
			}
			if err := h.history.reserve(row, previous.RequestID); err != nil {
				return err
			}
			reserved = true
			execCtx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
			defer stop()
			status, owned := h.apply(execCtx, path, stack, fields["selectionDigest"], id, qualifiedTarget, expected)
			return h.history.finish(status, owned)
		})
	}
	if !reserved && errors.Is(err, errHistoryCapacity) {
		w.Header().Set(journalCapacityHeader, "low")
		http.Error(w, errHistoryCapacity.Error(), http.StatusInsufficientStorage)
		return
	}
	if err != nil && !reserved {
		http.Error(w, "deployment not admitted; review the target and check operation status", 409)
		return
	}
	status, ok := h.history.status(id)
	if !ok {
		http.Error(w, "deployment outcome unavailable", 503)
		return
	}
	writeDeploymentStatus(w, status)
}

func writeDeploymentStatus(w http.ResponseWriter, status deploymentStatus) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func cliApply(exe, pkl, socket, daemon, state string) func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
	return cliApplyFor("", exe, pkl, socket, daemon, state)
}
func cliApplyFor(lifecycle, exe, pkl, socket, daemon, state string) func(context.Context, string, string, string, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
	return func(ctx context.Context, path, stack, selection, id, qualifiedTarget string, expected operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource) {
		unknown := deploymentStatus{RequestID: id, Stack: stack, State: "unknown", Effects: "unknown"}
		action := "up"
		if expected.Mode == "remove" {
			action = "down"
		}
		if lifecycle != "" {
			if !validLifecycleAction(lifecycle) || expected.Mode != "update" {
				return unknown, nil
			}
			action = lifecycle
		}
		unknown.Action = lifecycle
		input, err := json.Marshal(expected)
		if err != nil || !expected.Valid() || !preflight.ValidDigest(selection) {
			return unknown, nil
		}
		cmd := exec.CommandContext(ctx, exe, deploymentCLIArgs(action, path, stack, socket, daemon, state, selection)...)
		cmd.Dir, cmd.Env, cmd.WaitDelay = filepath.Dir(path), []string{"DILL_PKL_PATH=" + pkl}, 2*time.Second
		var out limitedPreviewOutput
		cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(input), &out, io.Discard
		err = cmd.Run()
		if ctx.Err() != nil {
			return unknown, nil
		}
		var exit *exec.ExitError
		if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
			return unknown, nil
		}
		var result operation.Result
		dec := json.NewDecoder(&out.buf)
		dec.DisallowUnknownFields()
		if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || result.Version != operation.Version || result.Operation != action || result.Stack != stack || result.Target == nil ||
			result.Target.Engine != "docker" || result.Target.EndpointDigest != operation.Digest(socket) || result.Target.QualifiedIdentityDigest != qualifiedTarget ||
			result.Resources == nil || result.Errors == nil || result.ResourcesTruncated || !strings.HasPrefix(result.OperationID, "op_") || !validApplyID(strings.TrimPrefix(result.OperationID, "op_")) {
			return unknown, nil
		}
		done := deploymentStatus{RequestID: id, Stack: stack, OperationID: result.OperationID, Effects: result.Effects, Action: lifecycle}
		switch {
		case err == nil && result.Outcome == "succeeded" && (result.Effects == "complete" || (validLifecycleAction(action) && result.Effects == "none")) && len(result.Errors) == 0 && (len(result.Resources) > 0 || (validLifecycleAction(action) && result.Effects == "none")) && result.ManagedResources != nil:
			owned := *result.ManagedResources
			if (action == "down" && len(owned) != 0) || (action != "down" && !(operation.ManagedExpectation{Mode: "update", Resources: owned}).Valid()) {
				return unknown, nil
			}
			done.State = "succeeded"
			return done, owned
		case err != nil && (result.Outcome == "failed" || result.Outcome == "cancelled") && (result.Effects == "none" || result.Effects == "partial") && len(result.Errors) > 0 && result.ManagedResources == nil:
			done.State = "failed"
			return done, nil
		default:
			return unknown, nil
		}
	}
}
