package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

func runVolumes(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("volumes", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "volumes"); err != nil {
		return err
	}

	st, err := resolveLabelStack(ctx, opts)
	if err != nil {
		return err
	}
	engine, err := newEngine(ctx, st.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStackVolumes(ctx, st.Name)
	if err != nil {
		return fmt.Errorf("listing volumes: %w", err)
	}
	if names == nil {
		names = []string{}
	}
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(names)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME")
	for _, name := range names {
		fmt.Fprintln(w, name)
	}
	return w.Flush()
}
