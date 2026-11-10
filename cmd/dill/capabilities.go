package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/umayr/dill/internal/orchestrator"
)

func runCapabilities(opts commandOptions, args []string) error {
	fs := flag.NewFlagSet("capabilities", flag.ContinueOnError)
	addEngineFlag(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(fs.Args(), "capabilities"); err != nil {
		return err
	}
	caps, err := orchestrator.DiscoverCapabilities(opts.Engine)
	if err != nil {
		return usageError{err.Error()}
	}
	return json.NewEncoder(os.Stdout).Encode(caps)
}
