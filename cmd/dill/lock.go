package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/imagelock"
)

// runLock reserves the output before reading the previous lock, so concurrent
// updates cannot overwrite one another. A leftover reservation requires manual
// inspection and removal; it is never assumed to belong to a dead process.
func runLock(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("lock", flag.ContinueOnError)
	file := flags.String("file", "compose.pkl", "configuration file")
	update := flags.String("update", "", "service or job to refresh")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	cfg, err := config.LoadForImageLock(ctx, *file)
	if err != nil {
		return err
	}
	if config.HasSecretDeclarations(cfg) {
		return fmt.Errorf("image lock does not support secret configuration")
	}
	services, jobs := config.ImageReferences(cfg)
	var selectedServices, selectedJobs map[string]bool
	if *update != "" {
		selectedServices, selectedJobs = map[string]bool{}, map[string]bool{}
		_, service := services[*update]
		_, job := jobs[*update]
		if !service && !job {
			return fmt.Errorf("unknown service or job %q", *update)
		}
		if service {
			selectedServices[*update] = true
		}
		if job {
			selectedJobs[*update] = true
		}
	}
	path := filepath.Join(filepath.Dir(*file), "dill.lock")
	reservation := path + ".reservation"
	f, err := os.OpenFile(reservation, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("lock reservation %s exists (another writer or stale reservation); inspect it before removal", reservation)
		}
		return err
	}
	defer os.Remove(reservation)
	if err := f.Close(); err != nil {
		return err
	}
	previous, err := config.ReadImageLock(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lock, err := imagelock.Build(ctx, services, jobs, previous, selectedServices, selectedJobs, nil)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".dill.lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
