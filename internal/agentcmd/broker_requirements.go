package agentcmd

import (
	"context"
	"encoding/json"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/preflight"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"
)

func (c brokerCLI) requirements(ctx context.Context, path, stack string) (brokersecret.Requirements, error) {
	cmd := exec.CommandContext(ctx, c.exe, "broker-requirements", "--contract", brokersecret.Version, "-f", path, "-p", stack)
	cmd.Dir = filepath.Dir(path)
	cmd.Env = []string{"DILL_PKL_PATH=" + c.pkl}
	cmd.WaitDelay = 2 * time.Second
	var out limitedPreviewOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	var result brokersecret.Requirements
	if cmd.Run() != nil || ctx.Err() != nil || brokersecret.DecodeMetadata(&out.buf, &result) != nil || !result.Valid() || result.Engine != brokerEngine(c.engine) || result.Stack != stack {
		return brokersecret.Requirements{}, brokersecret.ErrUnavailable
	}
	return result, nil
}
func (h *brokerEndpoint) serveRequirements(w http.ResponseWriter, r *http.Request, stage string) {
	fields, ok := deploymentFields(w, r, "stack", "manifestSha256")
	if !ok || !validDeployStack(fields["stack"]) || !preflight.ValidDigest("sha256:"+fields["manifestSha256"]) {
		http.Error(w, "invalid requirements request", 400)
		return
	}
	if h.requirements == nil {
		http.Error(w, "requirements unavailable", 503)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "deployment busy", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result brokersecret.Requirements
	err := h.stages.WithVerifiedEntrypoint(ctx, stage, fields["manifestSha256"], func(path string) error {
		var err error
		result, err = h.requirements(ctx, path, fields["stack"])
		return err
	})
	if err != nil || ctx.Err() != nil || !result.Valid() || result.Stack != fields["stack"] || result.Engine != brokerEngine(h.engine) {
		http.Error(w, "requirements unavailable", 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Contract     string                    `json:"contract"`
		Target       string                    `json:"targetDigest"`
		Endpoint     string                    `json:"endpointDigest"`
		Source       string                    `json:"sourceDigest"`
		Requirements brokersecret.Requirements `json:"requirements"`
	}{brokersecret.Version, h.target, h.endpoint, "sha256:" + fields["manifestSha256"], result})
}
