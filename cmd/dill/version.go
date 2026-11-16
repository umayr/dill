package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

func runVersion(args []string) (retErr error) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	defer func() {
		var usage usageError
		if *format == "json" && errors.As(retErr, &usage) {
			if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"error": retErr.Error(), "kind": "invalid_input"}); err != nil {
				retErr = fmt.Errorf("rendering failure: %w", err)
			}
		}
	}()
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if err := validateFormat(*format, "text", "json"); err != nil {
		return err
	}
	if err := ensureNoArgs(fs.Args(), "version"); err != nil {
		return err
	}
	if *format == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Version string `json:"version"`
		}{version}); err != nil {
			// Output errors are operational failures, not invalid input.
			return fmt.Errorf("rendering failure: %w", err)
		}
		return nil
	}
	_, err := fmt.Fprintln(os.Stdout, version)
	return err
}
