package config

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

type privateEvaluationKey struct{}

// WithPrivateEvaluation selects bounded, fixed-error Pkl evaluation for a
// launcher-known secret path. It is not Pkl sandboxing or general taint tracking.
func WithPrivateEvaluation(ctx context.Context) context.Context {
	return context.WithValue(ctx, privateEvaluationKey{}, true)
}

const privatePklOutputLimit = 8 * 1024 * 1024
const privatePklDiagnosticLimit = 64 * 1024

type privateEvalBuffer struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *privateEvalBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.buffer.Len() {
		b.cancel()
		return 0, ErrSecretUnavailable
	}
	return b.buffer.Write(p)
}

func privatePklOutput(ctx context.Context, pkl, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stdout := &privateEvalBuffer{limit: privatePklOutputLimit, cancel: cancel}
	stderr := &privateEvalBuffer{limit: privatePklDiagnosticLimit, cancel: cancel}
	cmd := exec.CommandContext(ctx, pkl, "eval", path)
	cmd.WaitDelay = time.Second
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if cmd.Run() != nil || ctx.Err() != nil {
		return nil, ErrSecretUnavailable
	}
	return stdout.buffer.Bytes(), nil
}
