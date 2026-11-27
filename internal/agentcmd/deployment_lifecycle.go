package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/umayr/dill/internal/preflight"
	"net/http"
	"strings"
	"time"
)

func validLifecycleAction(action string) bool {
	return action == "start" || action == "stop" || action == "restart"
}
func lifecyclePath(path string) string {
	for _, action := range []string{"start", "stop", "restart"} {
		if path == "/v1/deploy/"+action || path == "/v1/deploy/"+action+"-preview" {
			return action
		}
	}
	return ""
}
func (h *applyEndpoint) serveLifecycle(w http.ResponseWriter, r *http.Request, action string) {
	preview, execute := h.lifecyclePreview[action], h.lifecycleApply[action]
	if preview == nil || execute == nil {
		http.NotFound(w, r)
		return
	}
	isPreview := strings.HasSuffix(r.URL.Path, "-preview")
	var fields map[string]string
	var ok bool
	if isPreview {
		fields, ok = deploymentFields(w, r, "stack")
	} else {
		fields, ok = deploymentFields(w, r, "stack", "selectionDigest", "requestId", "priorRequestId", "confirm")
	}
	if !ok || !validDeployStack(fields["stack"]) {
		http.Error(w, "invalid lifecycle request", 400)
		return
	}
	stack := fields["stack"]
	previous, mode, plain := h.history.managedPlain(stack)
	if !plain {
		http.Error(w, "broker review required", http.StatusConflict)
		return
	}
	if mode != "update" || previous.RequestID == "" {
		http.Error(w, "managed deployment unavailable or uncertain", 409)
		return
	}
	expected := ownership("update", previous)
	if isPreview {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err := h.stages.WithVerifiedEntrypoint(ctx, previous.StageID, previous.Manifest, func(path string) error {
			result, e := preview(ctx, path, stack)
			if e != nil || result.Operation != action || result.SelectionDigest == nil || result.Target == nil || !expected.Matches(result.Resources) || ctx.Err() != nil {
				return errOperationConflict
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Dill-Prior-Request", previous.RequestID)
			w.Header().Set("X-Dill-Deployment-Mode", action)
			return json.NewEncoder(w).Encode(result)
		})
		if err != nil {
			http.Error(w, "lifecycle preview unavailable", 409)
		}
		return
	}
	if fields["confirm"] != action || !preflight.ValidDigest(fields["selectionDigest"]) || !validApplyID(fields["requestId"]) || fields["priorRequestId"] != previous.RequestID {
		http.Error(w, "invalid lifecycle confirmation", 409)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "deployment busy", 409)
		return
	}
	id := fields["requestId"]
	if _, exists := h.history.status(id); exists {
		http.Error(w, "request already recorded; inspect status", 409)
		return
	}
	row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: id, Stack: stack, Action: action, State: "pending", Effects: "unknown"}, Mode: "update", StageID: previous.StageID, Manifest: previous.Manifest}
	reserved := false
	var qualified string
	verify, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err := h.stages.WithVerifiedEntrypoint(verify, previous.StageID, previous.Manifest, func(path string) error {
		result, e := preview(verify, path, stack)
		if e != nil || result.Operation != action || result.SelectionDigest == nil || *result.SelectionDigest != fields["selectionDigest"] || result.Target == nil || !expected.Matches(result.Resources) || verify.Err() != nil {
			return errOperationConflict
		}
		qualified = result.Target.QualifiedIdentityDigest
		return nil
	})
	if err == nil {
		err = h.stages.WithVerifiedEntrypoint(verify, previous.StageID, previous.Manifest, func(path string) error {
			if verify.Err() != nil {
				return verify.Err()
			}
			if e := h.history.reserve(row, previous.RequestID); e != nil {
				return e
			}
			reserved = true
			ctx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
			defer stop()
			status, owned := execute(ctx, path, stack, fields["selectionDigest"], id, qualified, expected)
			status.Action = action
			return h.history.finish(status, owned)
		})
	}
	if !reserved {
		if errors.Is(err, errHistoryCapacity) {
			http.Error(w, "journal capacity reached", 507)
		} else {
			http.Error(w, "lifecycle not admitted; inspect status", 409)
		}
		return
	}
	status, found := h.history.status(id)
	if !found {
		http.Error(w, "lifecycle outcome unavailable", 503)
		return
	}
	writeDeploymentStatus(w, status)
}
