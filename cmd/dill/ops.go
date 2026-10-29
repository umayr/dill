package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/loader"
	"github.com/umayr/dill/internal/orchestrator"
)

// errDoctorFoundIssues signals that one or more doctor checks failed. The
// report is already rendered; the caller exits non-zero without extra output.
var errDoctorFoundIssues = errors.New("doctor found issues")

func runStatus(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "status"); err != nil {
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
	type statusOutput struct {
		Stack      string                          `json:"stack"`
		Engine     string                          `json:"engine"`
		ConfigFile string                          `json:"config_file"`
		Services   int                             `json:"services"`
		Containers []*orchestrator.ContainerStatus `json:"containers"`
	}
	out := statusOutput{
		Stack:      sn,
		Engine:     cfg.Engine,
		ConfigFile: opts.ConfigFile,
		Services:   len(cfg.Services),
	}
	for _, name := range names {
		cs, err := engine.ServiceStatus(ctx, name)
		if err != nil {
			cs = &orchestrator.ContainerStatus{Name: name, State: "error", Status: err.Error()}
		}
		out.Containers = append(out.Containers, cs)
	}
	if out.Containers == nil {
		out.Containers = []*orchestrator.ContainerStatus{}
	}
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	running := 0
	for _, cs := range out.Containers {
		if cs.State == "running" {
			running++
		}
	}
	fmt.Printf("Stack: %s\nEngine: %s\nConfig: %s\nServices: %d\nContainers: %d running, %d stopped/missing\n\n",
		out.Stack, out.Engine, out.ConfigFile, out.Services, running, len(out.Containers)-running)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tSTATUS\tIMAGE\tPORTS")
	for _, cs := range out.Containers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", cs.Name, cs.State, cs.Status, cs.Image, strings.Join(cs.Ports, ", "))
	}
	return w.Flush()
}

func runDoctor(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "doctor"); err != nil {
		return err
	}

	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail,omitempty"`
	}
	checks := []check{}
	add := func(name string, err error, detail string) {
		c := check{Name: name, OK: err == nil, Detail: detail}
		if err != nil {
			c.Detail = err.Error()
		}
		checks = append(checks, c)
	}

	pkl, err := loader.FindPkl(ctx)
	add("pkl", err, pkl)

	cfg, err := loadConfig(ctx, opts)
	add("config", err, opts.ConfigFile)
	if err == nil {
		add("validate", config.Validate(ctx, opts.ConfigFile), opts.ConfigFile)
		engine, err := openEngine(ctx, cfg)
		add("engine", err, cfg.Engine)
		if err == nil {
			engine.Close()
		}
	} else {
		engineName := opts.Engine
		if engineName == "" {
			engineName = "podman"
		}
		engine, err := newEngine(ctx, engineName)
		add("engine", err, engineName)
		if err == nil {
			engine.Close()
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(checks)
	}
	ok := true
	for _, c := range checks {
		status := "ok"
		if !c.OK {
			status = "fail"
			ok = false
		}
		fmt.Printf("%-10s %s", status, c.Name)
		if c.Detail != "" {
			fmt.Printf(" (%s)", c.Detail)
		}
		fmt.Println()
	}
	if !ok {
		return errDoctorFoundIssues
	}
	return nil
}

func runStacks(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("stacks", flag.ContinueOnError)
	addEngineFlag(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "stacks"); err != nil {
		return err
	}
	engineName := opts.Engine
	if engineName == "" {
		engineName = "podman"
	}
	engine, err := newEngine(ctx, engineName)
	if err != nil {
		return err
	}
	defer engine.Close()
	stacks, err := engine.ListStacks(ctx)
	if err != nil {
		return fmt.Errorf("listing stacks: %w", err)
	}
	if *format == "json" {
		if stacks == nil {
			stacks = []orchestrator.StackStatus{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stacks)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tENGINE\tCONTAINERS\tRUNNING\tSTOPPED")
	for _, st := range stacks {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n", st.Name, st.Engine, st.Containers, st.Running, st.Stopped)
	}
	return w.Flush()
}

func runRm(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	if len(args) > 0 {
		if _, err := selectServices(cfg.Services, args, false); err != nil {
			return err
		}
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()

	names := []string{}
	if len(args) > 0 {
		for _, name := range args {
			names = append(names, serviceContainerName(sn, name, cfg.Services[name]))
		}
	} else {
		names, err = engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
	}
	if len(names) == 0 {
		if !opts.Quiet {
			fmt.Printf("no containers found for stack %s\n", sn)
		}
		return nil
	}
	if !opts.DryRun {
		ok, err := confirmDestructive(fmt.Sprintf("This will remove stopped containers for stack %s", sn), opts)
		if err != nil {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		if !ok {
			if !opts.Quiet {
				fmt.Fprintln(os.Stderr, "aborted")
			}
			return nil
		}
	}
	for _, name := range names {
		cs, err := engine.ServiceStatus(ctx, name)
		if err != nil {
			if errors.Is(err, orchestrator.ErrNotFound) {
				continue
			}
			return err
		}
		if cs.State == "running" {
			if !opts.Quiet {
				fmt.Printf("skipping running container %s\n", name)
			}
			continue
		}
		if opts.DryRun {
			fmt.Printf("would remove %s\n", name)
			continue
		}
		if !opts.Quiet {
			printProgress(name, "removing")
		}
		if err := engine.RemoveService(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func runWait(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	addTimeoutFlag(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()

	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	services, err := selectServices(cfg.Services, args, false)
	if err != nil {
		return err
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	for name, svc := range services {
		containerName := serviceContainerName(sn, name, svc)
		for {
			ready, err := engine.IsReady(ctx, containerName, svc.HealthCheck != nil)
			if err != nil {
				return fmt.Errorf("service %q readiness check: %w", name, err)
			}
			if ready {
				if !opts.Quiet {
					printProgress(name, "ready")
				}
				break
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return fmt.Errorf("service %q did not become ready within timeout", name)
			}
		}
	}
	return nil
}

func runPort(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("port", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return usageError{"usage: dill port [flags] <service> [container-port]"}
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	service := fs.Arg(0)
	if _, ok := cfg.Services[service]; !ok {
		return usageError{fmt.Sprintf("unknown service %q", service)}
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()
	cs, err := engine.ServiceStatus(ctx, serviceContainerName(sn, service, cfg.Services[service]))
	if err != nil {
		return err
	}
	ports := cs.Ports
	if fs.NArg() == 2 {
		needle := fs.Arg(1)
		filtered := ports[:0]
		for _, p := range ports {
			if strings.Contains(p, "->"+needle+"/") || strings.Contains(p, "->"+needle) {
				filtered = append(filtered, p)
			}
		}
		ports = filtered
	}
	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ports)
	}
	for _, p := range ports {
		fmt.Println(p)
	}
	return nil
}

func runTop(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()
	if len(args) < 1 {
		return usageError{"usage: dill top <service> [engine args...]"}
	}
	cfg, err := loadConfig(ctx, opts)
	if err != nil {
		return err
	}
	service := args[0]
	if _, ok := cfg.Services[service]; !ok {
		return usageError{fmt.Sprintf("unknown service %q", service)}
	}
	sn := resolveStackNameWithOptions(cfg, opts)
	engine, err := openEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer engine.Close()
	containerName := serviceContainerName(sn, service, cfg.Services[service])
	binary := engineBinary(engine)
	cmdArgs := append([]string{"top", containerName}, args[1:]...)
	c := exec.CommandContext(ctx, binary, cmdArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func runCp(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()
	if len(args) != 2 {
		return usageError{"usage: dill cp <service>:<path> <host-path> | <host-path> <service>:<path>"}
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
	rewritten := append([]string{}, args...)
	for i, arg := range rewritten {
		service, path, ok := strings.Cut(arg, ":")
		if !ok {
			continue
		}
		svc, exists := cfg.Services[service]
		if !exists {
			return usageError{fmt.Sprintf("unknown service %q", service)}
		}
		rewritten[i] = serviceContainerName(sn, service, svc) + ":" + path
	}
	binary := engineBinary(engine)
	cmdArgs := append([]string{"cp"}, rewritten...)
	c := exec.CommandContext(ctx, binary, cmdArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
