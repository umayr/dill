package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
)

func runImages(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("images", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "images"); err != nil {
		return err
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}

	type row struct {
		Service string `json:"service"`
		Image   string `json:"image"`
	}
	rows := make([]row, 0, len(cfg.Services))
	for name, svc := range cfg.Services {
		rows = append(rows, row{Service: name, Image: svc.Image})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Service < rows[j].Service })
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tIMAGE")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\n", r.Service, r.Image)
	}
	return w.Flush()
}
