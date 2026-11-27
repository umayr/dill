package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"io"
	"path/filepath"
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
	fs := flag.NewFlagSet("broker-requirements", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	values := map[string]string{}
	for _, name := range []string{"contract", "f", "p"} {
		key := name
		fs.Func(key, "", func(value string) error {
			if _, exists := values[key]; exists {
				return brokersecret.ErrInvalid
			}
			values[key] = value
			return nil
		})
	}
	if fs.Parse(args) != nil || fs.NArg() != 0 || len(values) != 3 || values["contract"] != brokersecret.Version || !filepath.IsAbs(values["f"]) || filepath.Clean(values["f"]) != values["f"] {
		return fail()
	}
	cfg, err := load(ctx, values["f"])
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	desc, err := brokersecret.Describe(cfg, values["p"])
	if err != nil {
		return fail()
	}
	if json.NewEncoder(out).Encode(desc) != nil {
		return protocolExit{1}
	}
	return nil
}
