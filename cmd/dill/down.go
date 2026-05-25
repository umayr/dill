package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
)

func runDown(ctx context.Context, configFile string, services []string) error {
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

	// If specific services are named, stop + remove only those containers
	// without tearing down the network.
	if len(services) > 0 {
		for _, svc := range services {
			containerName := sn + "_" + svc
			if s, ok := cfg.Services[svc]; ok && s.ContainerName != "" {
				containerName = s.ContainerName
			}
			printProgress(svc, "stopping")
			if err := engine.StopService(ctx, containerName); err != nil {
				logger.Debug("stop failed", "name", containerName, "err", err)
			}
			printProgress(svc, "removing")
			if err := engine.RemoveService(ctx, containerName); err != nil {
				logger.Warn("remove failed", "name", containerName, "err", err)
			}
		}
		return nil
	}

	return dag.Down(ctx, engine, sn, downProgress(sn))
}

func runTeardown(ctx context.Context, configFile string, args []string) error {
	tfs := flag.NewFlagSet("teardown", flag.ExitOnError)
	force := tfs.Bool("force", false, "force-remove images even if other containers reference them")
	if err := tfs.Parse(args); err != nil {
		return err
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

	if err := dag.Down(ctx, engine, sn, downProgress(sn)); err != nil {
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
			printProgress(v.Source, "removing volume")
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
		printProgress(svc.Image, "removing image")
		if err := engine.RemoveImage(ctx, svc.Image, *force); err != nil {
			logger.Warn("remove image failed", "image", svc.Image, "err", err)
		}
	}

	return nil
}
