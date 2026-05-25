package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/umayr/dill/internal/config"
)

func runPs(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	sn := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, sn)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSTATE\tSTATUS\tIMAGE\tPORTS")
	for _, name := range names {
		cs, err := engine.ServiceStatus(ctx, name)
		if err != nil {
			fmt.Fprintf(w, "%s\t-\t-\terror: %s\t-\t-\n", name, err)
			continue
		}
		ports := strings.Join(cs.Ports, ", ")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			cs.Name, cs.ID, cs.State, cs.Status, cs.Image, ports)
	}
	return w.Flush()
}
