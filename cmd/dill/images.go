package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/umayr/dill/internal/config"
)

func runImages(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tIMAGE")
	for _, name := range names {
		fmt.Fprintf(w, "%s\t%s\n", name, cfg.Services[name].Image)
	}
	return w.Flush()
}
