package main

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
)

func runBrokerRequirements(ctx context.Context, args []string, out io.Writer) error {
	return brokerRequirementsWith(ctx, args, out, func(ctx context.Context, path string) (*config.DillConfig, error) {
		scoped, err := brokerInputContext(ctx, path)
		if err != nil {
			return nil, err
		}
		return config.Load(scoped, path)
	})
}
func brokerRequirementsWith(ctx context.Context, args []string, out io.Writer, load func(context.Context, string) (*config.DillConfig, error)) error {
	fail := func() error {
		_ = json.NewEncoder(out).Encode(struct{ Contract, Status, Error string }{brokersecret.Version, "unavailable", "Secret requirements unavailable."})
		return protocolExit{1}
	}
	if ctx == nil || out == nil || load == nil {
		return protocolExit{1}
	}
	var contract, file, stack string
	fields := map[string]*string{"contract": &contract, "f": &file, "p": &stack}
	n, err := parseBrokerFlags(args, fields, true)
	if err != nil || n != len(fields) || contract != brokersecret.Version || !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return fail()
	}
	cfg, err := load(ctx, file)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	desc, err := brokersecret.Describe(cfg, stack)
	if err != nil {
		return fail()
	}
	if json.NewEncoder(out).Encode(desc) != nil {
		return protocolExit{1}
	}
	return nil
}
