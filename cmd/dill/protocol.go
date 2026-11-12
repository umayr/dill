package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/umayr/dill/internal/protocol"
)

// Detect the protocol command before legacy argument parsing, logging or env
// setup. Skip known global values, never scan exec/container argv as commands.
func isProtocolInvocation(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == cmdProtocol {
			return true
		}
		if strings.HasPrefix(a, "-") {
			name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if !inline {
				switch name {
				case "f", "p", "stack-name", "project-name", "engine", "engine-socket", "timeout", "env-file", "secret-target", "secret-bindings", "proof-key-file", "proof-file":
					i++
				}
			}
			continue
		}
		for _, cmd := range allCommands {
			if a == cmd {
				return false
			}
		}
		// A malformed prefix before protocol must not route secrets into flag/usage
		// diagnostics. Known legacy commands above keep their existing semantics.
		for _, rest := range args[i+1:] {
			if rest == cmdProtocol {
				return true
			}
		}
		return false
	}
	return false
}

func runProtocol(args []string) int {
	mode := protocol.InvalidMode
	if len(args) == 1 && args[0] == cmdProtocol {
		mode = protocol.DiscoveryMode
	}
	if len(args) == 2 && args[0] == cmdProtocol && args[1] == cmdProtocolRun {
		mode = protocol.CheckMode
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return protocol.Serve(ctx, mode, protocol.NewTransport(os.Stdin, os.Stdout, os.Stderr))
}

// Structured protocol failures have already been reported (or cannot safely be
// delivered). They must never pass through legacy raw-error logging.
type protocolExit struct{ code int }

func (protocolExit) Error() string { return "protocol command failed" }
