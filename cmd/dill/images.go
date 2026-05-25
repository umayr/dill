package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/umayr/dill/internal/config"
)

func runImages(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tIMAGE")
	for name, svc := range cfg.Services {
		fmt.Fprintf(w, "%s\t%s\n", name, svc.Image)
	}
	return w.Flush()
}
