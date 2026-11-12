package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"

	"github.com/umayr/dill/internal/orchestrator"
)

func runCaps(opts commandOptions, args []string) error {
	fs := flag.NewFlagSet(cmdCaps, flag.ContinueOnError)
	if opts.secretMode() {
		fs.SetOutput(io.Discard)
	}
	addEngineFlag(fs, &opts)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := ensureNoArgs(fs.Args(), cmdCaps); err != nil {
		return err
	}
	caps, err := orchestrator.DiscoverCapabilities(opts.Engine)
	if err != nil {
		return usageError{err.Error()}
	}
	return json.NewEncoder(os.Stdout).Encode(caps)
}
