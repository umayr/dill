package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
)

func runPs(ctx context.Context, configFile string, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}

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

	var statuses []*orchestrator.ContainerStatus
	for _, name := range names {
		cs, err := engine.ServiceStatus(ctx, name)
		if err != nil {
			cs = &orchestrator.ContainerStatus{Name: name, State: "error", Status: err.Error()}
		}
		statuses = append(statuses, cs)
	}

	switch *format {
	case "json":
		if statuses == nil {
			statuses = []*orchestrator.ContainerStatus{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(statuses)
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tID\tSTATE\tSTATUS\tIMAGE\tPORTS")
		for _, cs := range statuses {
			ports := strings.Join(cs.Ports, ", ")
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				cs.Name, cs.ID, cs.State, cs.Status, cs.Image, ports)
		}
		return w.Flush()
	}
}
