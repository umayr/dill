package main

import (
	"context"
	"errors"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// Broker source inputs are catalogued source data, never arbitrary HTTP env.
// Preserve only evaluator infrastructure from operator policy; ambient BRINE_VAR_
// values cannot substitute for a missing input document or enter a capture.
func brokerInputContext(ctx context.Context, source string) (context.Context, error) {
	env := make([]string, 0)
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	fd, err := unix.Open(filepath.Join(filepath.Dir(source), brokersecret.InputsFile), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return config.WithScopedEnvironment(config.WithMachineEvaluation(ctx), env), nil
	}
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), "broker-inputs")
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > brokersecret.MaxInputs {
		return nil, brokersecret.ErrUnavailable
	}
	inputs, err := brokersecret.DecodeInputs(f)
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	vars, err := inputs.Environment()
	if err != nil {
		return nil, brokersecret.ErrUnavailable
	}
	env = append(env, vars...)
	return config.WithScopedEnvironment(config.WithMachineEvaluation(ctx), env), nil
}
