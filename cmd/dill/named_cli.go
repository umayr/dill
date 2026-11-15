package main

import (
	"encoding/json"
	"flag"
	"io"

	"github.com/umayr/dill/internal/preflight"
)

func runPreflightCapabilities(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("preflight-capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "")
	if fs.Parse(args) != nil || *format != "json" || fs.NArg() != 0 {
		return protocolExit{2}
	}
	if err := json.NewEncoder(out).Encode(preflight.Discovery(qualifiedNativeHost())); err != nil {
		return protocolExit{1}
	}
	return nil
}
