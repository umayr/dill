package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
)

func runDown(ctx context.Context, opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	addConfigFlags(fs, &opts)
	addActionFlags(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	args = fs.Args()

	st, err := resolveLabelStack(ctx, opts)
	if err != nil {
		return err
	}
	if len(args) > 0 {
		if err := st.requireConfig("down"); err != nil {
			return err
		}
	}
	sn := st.Name
	engine, err := newEngine(ctx, st.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	// If specific services are named, stop + remove only those containers
	// without tearing down the network.
	if len(args) > 0 {
		cfg := st.Config
		if _, err := selectServices(cfg.Services, args, false); err != nil {
			return err
		}
		for _, svc := range args {
			containerName := serviceContainerName(sn, svc, cfg.Services[svc])
			if opts.DryRun {
				fmt.Printf("would stop and remove %s\n", svc)
				continue
			}
			if !opts.Quiet {
				printProgress(svc, "stopping")
			}
			if err := engine.StopService(ctx, containerName); err != nil {
				logger.Debug("stop failed", "name", containerName, "err", err)
			}
			if !opts.Quiet {
				printProgress(svc, "removing")
			}
			if err := engine.RemoveService(ctx, containerName); err != nil {
				logger.Warn("remove failed", "name", containerName, "err", err)
			}
		}
		return nil
	}

	if opts.DryRun {
		names, err := engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
		for _, name := range names {
			fmt.Printf("would remove %s\n", name)
		}
		return nil
	}
	return dag.Down(ctx, engine, sn, opts.downProgress(sn))
}

func runTeardown(ctx context.Context, opts commandOptions, args []string) error {
	tfs := flag.NewFlagSet("teardown", flag.ContinueOnError)
	addConfigFlags(tfs, &opts)
	addActionFlags(tfs, &opts)
	force := tfs.Bool("force", false, "force-remove images even if other containers reference them")
	if err := tfs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(tfs.Args(), "teardown"); err != nil {
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

	vols, err := engine.ListStackVolumes(ctx, sn)
	if err != nil {
		logger.Warn("could not list stack volumes", "err", err)
		vols = nil
	}

	if !opts.DryRun {
		msg := fmt.Sprintf("This will remove containers, images, and network for stack %s", sn)
		if len(vols) > 0 {
			msg = fmt.Sprintf("This will remove containers, images, %d named volume(s), and network for stack %s\nvolumes: %s",
				len(vols), sn, strings.Join(vols, ", "))
		}
		ok, err := confirmDestructive(msg, opts)
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

	if opts.DryRun {
		fmt.Printf("would remove stack %s containers, network, and images\n", sn)
		if len(vols) > 0 {
			fmt.Printf("would remove volumes: %s\n", strings.Join(vols, ", "))
		}
		return nil
	}

	if err := dag.Down(ctx, engine, sn, opts.downProgress(sn)); err != nil {
		return err
	}

	for _, name := range vols {
		if !opts.Quiet {
			printProgress(name, "removing volume")
		}
		if err := engine.RemoveVolume(ctx, name, *force); err != nil {
			logger.Warn("remove volume failed", "name", name, "err", err)
		}
	}

	seenImg := make(map[string]bool)
	for _, svc := range cfg.Services {
		if svc.Image == "" || seenImg[svc.Image] {
			continue
		}
		seenImg[svc.Image] = true
		if !opts.Quiet {
			printProgress(svc.Image, "removing image")
		}
		if err := engine.RemoveImage(ctx, svc.Image, *force); err != nil {
			logger.Warn("remove image failed", "image", svc.Image, "err", err)
		}
	}

	return nil
}
