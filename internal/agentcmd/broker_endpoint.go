package agentcmd

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
	"net/http"
	"strings"
	"time"
)

const brokerContractHeader = "X-Dill-Broker-Contract"

type brokerEndpoint struct {
	stages           verifiedStage
	history          *deploymentHistory
	slot             chan struct{}
	target, endpoint string
	engine           string
	requirements     func(context.Context, string, string) (brokersecret.Requirements, error)
	prepare          func(context.Context, string, brokersecret.Envelope) (brokersecret.Review, error)
	retire           func(context.Context, string, brokersecret.Retirement) (brokersecret.Review, error)
	review           func(context.Context, string, brokersecret.Reference) (brokerCLIReply, error)
	apply            func(context.Context, string, brokersecret.Reference, string, string, operation.ManagedExpectation) (deploymentStatus, []operation.ManagedResource)
}

func (h *deploymentHistory) brokerProtected(stack string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.poisoned {
		return true
	}
	for _, r := range h.rows {
		if r.Stack == stack && r.Broker != nil {
			return true
		}
	}
	return false
}
func (h *deploymentHistory) brokerReference(id string) *brokersecret.Reference {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.rows {
		if r.RequestID == id && r.Broker != nil {
			ref := *r.Broker
			return &ref
		}
	}
	return nil
}

type brokerApplyRequest struct {
	brokersecret.Reference
	SelectionDigest string `json:"selectionDigest"`
	RequestID       string `json:"requestId"`
	Confirm         string `json:"confirm"`
}

func (h *brokerEndpoint) scopeOK(s brokersecret.Scope) bool {
	return s.Valid() && s.Engine == brokerEngine(h.engine) && s.TargetDigest == h.target && s.EndpointDigest == h.endpoint
}
func (h *brokerEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if len(r.Header.Values(brokerContractHeader)) != 1 || r.Header.Get(brokerContractHeader) != brokersecret.Version || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		http.Error(w, "invalid broker contract", 400)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/broker-secrets/journal" {
		if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.Error(w, "invalid broker request", 400)
			return
		}
		w.Header().Set(journalHeader, h.history.journal)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Contract string `json:"contract"`
			Journal  string `json:"journalId"`
			Target   string `json:"targetDigest"`
			Endpoint string `json:"endpointDigest"`
		}{brokersecret.Version, h.history.journal, h.target, h.endpoint})
		return
	}
	journal := r.Header.Values(journalHeader)
	if len(journal) != 1 || journal[0] != h.history.journal {
		http.Error(w, "deployment journal does not match", 409)
		return
	}
	w.Header().Set(journalHeader, h.history.journal)
	if h.history.nearCapacity() {
		w.Header().Set(journalCapacityHeader, "low")
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/broker-secrets/operations/") {
		id := strings.TrimPrefix(r.URL.Path, "/v1/broker-secrets/operations/")
		if !validApplyID(id) || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			http.NotFound(w, r)
			return
		}
		status, ok := h.history.status(id)
		ref := h.history.brokerReference(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Contract  string                  `json:"contract"`
			Status    deploymentStatus        `json:"status"`
			Reference *brokersecret.Reference `json:"reference"`
		}{brokersecret.Version, status, ref})
		return
	}
	var kind, stage string
	for _, k := range []string{"requirements", "prepare", "retire", "review", "apply"} {
		if id, ok := strings.CutPrefix(r.URL.Path, "/v1/broker-secrets/"+k+"/"); ok {
			kind, stage = k, id
		}
	}
	if r.Method != http.MethodPost || !validApplyID(stage) || kind == "" {
		http.NotFound(w, r)
		return
	}
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid broker request", 400)
		return
	}
	if kind == "requirements" {
		h.serveRequirements(w, r, stage)
		return
	}
	var e brokersecret.Envelope
	var request brokerApplyRequest
	var scope brokersecret.Scope
	var retirement brokersecret.Retirement
	if kind == "retire" {
		var err error
		retirement, err = brokersecret.DecodeRetirement(http.MaxBytesReader(w, r.Body, brokersecret.MaxMetadata))
		if err != nil {
			http.Error(w, "invalid broker retirement", 400)
			return
		}
		scope = retirement.Scope
	} else if kind == "prepare" {
		var err error
		e, err = brokersecret.Decode(http.MaxBytesReader(w, r.Body, brokersecret.MaxWire))
		if err != nil {
			http.Error(w, "invalid broker delivery", 400)
			return
		}
		defer e.Close()
		scope = e.Scope
	} else {
		var decodeErr error
		if kind == "review" {
			decodeErr = brokersecret.DecodeMetadata(http.MaxBytesReader(w, r.Body, brokersecret.MaxMetadata), &request.Reference)
		} else {
			decodeErr = brokersecret.DecodeMetadata(http.MaxBytesReader(w, r.Body, brokersecret.MaxMetadata), &request)
		}
		if decodeErr != nil || !request.Reference.Valid() {
			http.Error(w, "invalid broker reference", 400)
			return
		}
		if kind == "review" && (request.RequestID != "" || request.SelectionDigest != "" || request.Confirm != "") {
			http.Error(w, "invalid broker review", 400)
			return
		}
		if kind == "apply" && (!validApplyID(request.RequestID) || !preflight.ValidDigest(request.SelectionDigest) || (request.Confirm != "create" && request.Confirm != "update")) {
			http.Error(w, "invalid broker confirmation", 400)
			return
		}
		scope = request.Scope
	}
	if !h.scopeOK(scope) {
		http.Error(w, "broker target does not match", 409)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "deployment busy", 409)
		return
	}
	previous, mode := h.history.managed(scope.Stack)
	if mode == "" || (previous.Broker != nil && previous.Broker.Scope.Registration != scope.Registration) {
		http.Error(w, "managed ownership unavailable", 409)
		return
	}
	if kind == "retire" {
		included := false
		if previous.Broker != nil {
			for _, ref := range retirement.Previous {
				if ref == *previous.Broker {
					included = true
				}
			}
		}
		if h.retire == nil || !included {
			http.Error(w, "authenticated managed predecessor unavailable", 409)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	manifest := strings.TrimPrefix(scope.SourceDigest, "sha256:")
	if kind == "retire" {
		var review brokersecret.Review
		err := h.stages.WithVerifiedEntrypoint(ctx, stage, manifest, func(path string) error { var err error; review, err = h.retire(ctx, path, retirement); return err })
		if err != nil || review.Scope != scope || !brokersecret.ValidHandle(review.Handle) || review.Active || !review.ExpiresAt.After(time.Now()) || review.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
			http.Error(w, "broker retirement unavailable", 422)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Contract string              `json:"contract"`
			Prepared brokersecret.Review `json:"prepared"`
		}{brokersecret.Version, review})
		return
	}
	if kind == "prepare" {
		var review brokersecret.Review
		err := h.stages.WithVerifiedEntrypoint(ctx, stage, manifest, func(path string) error { var err error; review, err = h.prepare(ctx, path, e); return err })
		if err != nil || ctx.Err() != nil || review.Scope != scope || !brokersecret.ValidHandle(review.Handle) || review.Active {
			http.Error(w, "broker preparation unavailable", 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Contract string              `json:"contract"`
			Prepared brokersecret.Review `json:"prepared"`
		}{brokersecret.Version, review})
		return
	}
	expected := ownership(mode, previous)
	var preview brokerCLIReply
	err := h.stages.WithVerifiedEntrypoint(ctx, stage, manifest, func(path string) error {
		var err error
		preview, err = h.review(ctx, path, request.Reference)
		if err != nil || preview.Preview == nil || preview.Preview.SelectionDigest == nil || preview.Reference == nil || *preview.Reference != request.Reference || !expected.Matches(preview.Preview.Resources) {
			return errOperationConflict
		}
		return nil
	})
	if err != nil || ctx.Err() != nil {
		http.Error(w, "broker review unavailable", 409)
		return
	}
	if kind == "review" {
		w.Header().Set("X-Dill-Deployment-Mode", mode)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preview)
		return
	}
	if mode != request.Confirm || *preview.Preview.SelectionDigest != request.SelectionDigest {
		http.Error(w, "broker review changed", 409)
		return
	}
	reserved := false
	err = h.stages.WithVerifiedEntrypoint(ctx, stage, manifest, func(path string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ref := request.Reference
		row := deploymentRecord{deploymentStatus: deploymentStatus{RequestID: request.RequestID, Stack: scope.Stack, State: "pending", Effects: "unknown"}, Mode: mode, StageID: stage, Manifest: manifest, Broker: &ref}
		if err := h.history.reserve(row, previous.RequestID); err != nil {
			return err
		}
		reserved = true
		execCtx, stop := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
		defer stop()
		result, owned := h.apply(execCtx, path, ref, request.SelectionDigest, request.RequestID, expected)
		if result.RequestID != request.RequestID || result.Stack != scope.Stack {
			result = deploymentStatus{RequestID: request.RequestID, Stack: scope.Stack, State: "unknown", Effects: "unknown"}
			owned = nil
		}
		return h.history.finish(result, owned)
	})
	if !reserved {
		if errors.Is(err, errHistoryCapacity) {
			http.Error(w, "deployment journal capacity reached", 507)
		} else {
			http.Error(w, "broker deployment not admitted", 409)
		}
		return
	}
	// Failed or lost completion never becomes success: persisted pending reads unknown.
	status, ok := h.history.status(request.RequestID)
	if !ok {
		http.Error(w, "broker outcome unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Contract  string                 `json:"contract"`
		Status    deploymentStatus       `json:"status"`
		Reference brokersecret.Reference `json:"reference"`
	}{brokersecret.Version, status, request.Reference})
}
