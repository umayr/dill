package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/umayr/dill/internal/orchestrator"
)

func runPs(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	all := fs.Bool("all", false, "include configured services with missing containers")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "ps"); err != nil {
		return err
	}

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
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
	if *all {
		seen := make(map[string]bool, len(statuses))
		for _, cs := range statuses {
			seen[cs.Name] = true
		}
		for svcName, svc := range cfg.Services {
			containerName := serviceContainerName(sn, svcName, svc)
			if seen[containerName] {
				continue
			}
			statuses = append(statuses, &orchestrator.ContainerStatus{
				Name:   containerName,
				State:  "missing",
				Status: "not created",
				Image:  svc.Image,
			})
		}
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
