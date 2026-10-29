package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/umayr/dill/internal/config"
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

	// If specific services are named, stop + remove only those containers
	// without tearing down the network.
	if len(args) > 0 {
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

	if !opts.DryRun {
		ok, err := confirmDestructive(
			fmt.Sprintf("This will remove containers, images, named volumes, and network for stack %s", sn),
			opts,
		)
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
		fmt.Printf("would remove stack %s containers, network, named volumes, and images\n", sn)
		return nil
	}

	if err := dag.Down(ctx, engine, sn, opts.downProgress(sn)); err != nil {
		return err
	}

	seen := make(map[string]bool)
	for svcName, svc := range cfg.Services {
		vols, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			logger.Warn("could not parse volumes", "service", svcName, "err", err)
			continue
		}
		for _, v := range vols {
			if v.Type != "volume" || v.Source == "" || seen[v.Source] {
				continue
			}
			seen[v.Source] = true
			if !opts.Quiet {
				printProgress(v.Source, "removing volume")
			}
			if err := engine.RemoveVolume(ctx, v.Source, *force); err != nil {
				logger.Warn("remove volume failed", "name", v.Source, "err", err)
			}
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
