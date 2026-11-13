package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
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
	engine, err := newEngine(ctx, st.Engine, opts)
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
		targets := make([]orchestrator.RemovalTarget, 0, len(args))
		for _, svc := range args {
			targets = append(targets, orchestrator.RemovalTarget{Name: serviceContainerName(sn, svc, cfg.Services[svc]), Service: svc})
		}
		intent, err := orchestrator.CaptureRemovalIntent(ctx, engine, sn, targets)
		if err != nil {
			return err
		}
		if !opts.DryRun {
			var errs []error
			for _, resource := range intent.Resources {
				if !opts.Quiet {
					printProgress(resource.Service, "stopping")
				}
				if err := intent.Remove(ctx, engine, resource); err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		}
		for _, svc := range args {
			fmt.Fprintf(opts.output(), "would stop and remove %s\n", svc)
		}
		return nil
	}

	if opts.DryRun {
		names, err := engine.ListStack(ctx, sn)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
		for _, name := range names {
			fmt.Fprintf(opts.output(), "would remove %s\n", name)
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
	engine, err := openEngine(ctx, cfg, opts)
	if err != nil {
		return err
	}
	defer engine.Close()

	vols, listErr := engine.ListStackVolumes(ctx, sn)
	if listErr != nil {
		listErr = fmt.Errorf("list volumes for stack %q: %w", sn, listErr)
		if opts.operation != nil {
			return listErr
		}
	}

	names, err := engine.ListStack(ctx, sn)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	targets := make([]orchestrator.RemovalTarget, 0, len(names))
	for _, name := range names {
		targets = append(targets, orchestrator.RemovalTarget{Name: name})
	}
	intent, err := orchestrator.CaptureRemovalIntent(ctx, engine, sn, targets)
	if err != nil {
		return err
	}
	imageIDs := map[string]string{}
	var imageErrors []error
	if !opts.DryRun {
		for _, svc := range cfg.Services {
			if svc.Image == "" {
				continue
			}
			if _, seen := imageIDs[svc.Image]; seen {
				continue
			}
			id, err := engine.LocalImageID(ctx, svc.Image)
			if err != nil {
				id = ""
			}
			imageIDs[svc.Image] = id
			if err != nil {
				imageErrors = append(imageErrors, &orchestrator.ResourceError{Operation: "inspect image", Resource: svc.Image, Err: err})
			}
		}
	}
	networkAction := "retain the network"
	if !opts.DryRun {
		msg := fmt.Sprintf("This will remove containers and images and %s for stack %s\nContainers: %s", networkAction, sn, removalSummary(intent))
		for image, id := range imageIDs {
			if id != "" {
				msg += fmt.Sprintf("\nImage: %s (id %s)", image, id)
			}
		}
		if len(vols) > 0 {
			msg += fmt.Sprintf("\n%d named volume(s) will be retained: %s", len(vols), strings.Join(vols, ", "))
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
		fmt.Fprintf(opts.output(), "would remove stack %s containers and images and %s\n", sn, networkAction)
		if len(vols) > 0 {
			fmt.Fprintf(opts.output(), "would retain volumes: %s\n", strings.Join(vols, ", "))
		}
		return listErr
	}

	errs := []error{listErr, dag.DownIntent(ctx, engine, intent, opts.downProgress(sn))}
	errs = append(errs, imageErrors...)

	for _, name := range vols {
		if !opts.Quiet {
			printProgress(name, "checking retained volume")
		}
		if err := engine.RemoveVolume(ctx, name, sn, *force); err != nil {
			errs = append(errs, &orchestrator.ResourceError{Operation: "remove volume (resource may be retained)", Resource: name, Err: err})
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
		id := imageIDs[svc.Image]
		if id == "" {
			continue
		}
		currentID, err := engine.LocalImageID(ctx, svc.Image)
		if err != nil {
			errs = append(errs, &orchestrator.ResourceError{Operation: "inspect image", Resource: svc.Image, Err: err})
			continue
		}
		if currentID != id {
			errs = append(errs, &orchestrator.ResourceError{Operation: "image changed since removal intent; retained", Resource: svc.Image, ID: id, Err: orchestrator.ErrOwnershipConflict})
			continue
		}
		if err := engine.RemoveImage(ctx, id, *force); err != nil {
			errs = append(errs, &orchestrator.ResourceError{Operation: "remove image (resource may be retained)", Resource: svc.Image, ID: id, Err: err})
		}
	}

	return errors.Join(errs...)
}
