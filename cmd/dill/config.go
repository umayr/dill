package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func runConfig(ctx context.Context, opts commandOptions, args []string) error {
	cfs := flag.NewFlagSet("config", flag.ContinueOnError)
	addConfigFlags(cfs, &opts)
	format := cfs.String("format", "json", "output format: json or yaml")
	if err := cfs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "json", "yaml"); err != nil {
		return err
	}
	if err := ensureNoArgs(cfs.Args(), "config"); err != nil {
		return err
	}

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
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
	}

	_, err = os.Stdout.Write(out)
	return err
}
