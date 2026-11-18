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
	"time"

	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/preflight"
)

type verifiedStage interface {
	WithVerifiedEntrypoint(context.Context, string, string, func(string) error) error
}

type previewEndpoint struct {
	stages  verifiedStage
	preview func(context.Context, string, string) (preflight.Result, error)
}

func (h previewEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Values("Content-Type")) != 1 || r.Header.Get("Content-Type") != "application/json" || r.URL.RawQuery != "" {
		http.Error(w, "invalid preview request", http.StatusBadRequest)
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		http.Error(w, "invalid preview request", http.StatusBadRequest)
		return
	}
	fields := make(map[string]string, 3)
	for dec.More() {
		key, err := dec.Token()
		name, ok := key.(string)
		_, duplicate := fields[name]
		var value *string
		if err != nil || !ok || duplicate || (name != "stageId" && name != "manifestSha256" && name != "stack") || dec.Decode(&value) != nil || value == nil {
			http.Error(w, "invalid preview request", http.StatusBadRequest)
			return
		}
		fields[name] = *value
	}
	last, err := dec.Token()
	id, idErr := hex.DecodeString(fields["stageId"])
	if err != nil || last != json.Delim('}') || dec.Decode(new(any)) != io.EOF || len(fields) != 3 || !preflight.ValidDigest("sha256:"+fields["manifestSha256"]) ||
		idErr != nil || len(id) != 16 || hex.EncodeToString(id) != fields["stageId"] || !preflight.ValidName(fields["stack"]) {
		http.Error(w, "invalid preview request", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result preflight.Result
	err = h.stages.WithVerifiedEntrypoint(ctx, fields["stageId"], fields["manifestSha256"], func(path string) error {
		var err error
		result, err = h.preview(ctx, path, fields["stack"])
		return err
	})
	if err != nil || ctx.Err() != nil {
		http.Error(w, "deployment preview unavailable", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

type limitedPreviewOutput struct{ buf bytes.Buffer }

func (b *limitedPreviewOutput) Write(p []byte) (int, error) {
	if len(p) > preflight.MaxOutputBytes-b.buf.Len() {
		return 0, errors.New("deployment preview output exceeded limit")
	}
	return b.buf.Write(p)
}

// cliPreview runs exactly Dill's existing target-checked named preview. No
// project-selected engine/socket/process/flags are accepted. The executable is
// the running combined Dill binary, not a command resolved via PATH.
func cliPreview(executable, pkl, socket, daemon, state string) func(context.Context, string, string) (preflight.Result, error) {
	return func(ctx context.Context, path, stack string) (preflight.Result, error) {
		cmd := exec.CommandContext(ctx, executable, "preflight", "up", "--contract", preflight.Version,
			"-f", path, "-p", stack, "--engine", "docker", "--engine-socket", socket,
			"--coordination-layout", coordination.NativeDockerLayout, "--coordination-daemon-id", daemon,
			"--coordination-state-dir", state, "--coordination-source", "brine", "--no-prune", "--pull", "never", "--timeout", "25s")
		cmd.Dir = filepath.Dir(path)
		cmd.Env = []string{"DILL_PKL_PATH=" + pkl}
		cmd.WaitDelay = 2 * time.Second
		var out limitedPreviewOutput
		cmd.Stdout = &out
		cmd.Stderr = io.Discard
		var result preflight.Result
		if err := cmd.Run(); err != nil {
			return result, errors.New("deployment preview unavailable")
		}
		dec := json.NewDecoder(&out.buf)
		dec.DisallowUnknownFields()
		if dec.Decode(&result) != nil || dec.Decode(new(any)) != io.EOF || result.Validate(0) != nil || result.Operation != "up" || result.Stack != stack || result.SelectionDigest == nil || result.Target == nil || result.Target.EndpointDigest != operation.Digest(socket) {
			return preflight.Result{}, errors.New("invalid Dill preview result")
		}
		return result, nil
	}
}
