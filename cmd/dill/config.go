package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/umayr/dill/internal/config"
)

func runConfig(ctx context.Context, configFile string, args []string) error {
	cfs := flag.NewFlagSet("config", flag.ExitOnError)
	format := cfs.String("format", "json", "output format: json or yaml")
	if err := cfs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}

	switch *format {
	case "json":
		// already marshalled
	case "yaml":
		var v interface{}
		if err := json.Unmarshal(out, &v); err != nil {
			return fmt.Errorf("unmarshalling for yaml: %w", err)
		}
		out, err = yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshalling yaml: %w", err)
		}
	default:
		return fmt.Errorf("unknown format %q (supported: json, yaml)", *format)
	}

	_, err = os.Stdout.Write(out)
	return err
}
