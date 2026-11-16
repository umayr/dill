package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// runExternalCommand dispatches only an unambiguous top-level external
// command. Built-in commands always win. Global Dill options before the command
// are intentionally not supported for external commands: an extension owns its
// own flags and must not receive silently altered core configuration.
//
// Extensions are trusted executable programs, not sandboxed providers. In
// particular, the file-secret and proof workflows must never be handed off to
// an unknown executable.
func runExternalCommand(args []string) (int, bool) {
	if len(args) == 0 || !externalCommandName(args[0]) || secretInvocation(args) {
		return 0, false
	}
	for _, builtin := range allCommands {
		if args[0] == builtin {
			return 0, false
		}
	}
	// These namespaces are handled before the ordinary command switch.
	if args[0] == "preflight" || args[0] == "preflight-capabilities" {
		return 0, false
	}
	path, err := exec.LookPath("dill-" + args[0])
	if errors.Is(err, exec.ErrNotFound) {
		return 0, false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "dill: cannot locate external command %q: %v\n", args[0], err)
		return 1, true
	}
	cmd := exec.Command(path, args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if code := exit.ExitCode(); code >= 0 {
				return code, true
			}
			if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal()), true
			}
		}
		fmt.Fprintf(os.Stderr, "dill: external command %q failed: %v\n", args[0], err)
		return 1, true
	}
	return 0, true
}

func externalCommandName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
