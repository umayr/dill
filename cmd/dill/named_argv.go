package main

import (
	"os"
	"strings"
)

// namedFlags walks lexical flag tokens without treating a string option's
// value as another flag. This is only intent detection, not validation; the
// frozen strict parser still rejects every unknown/duplicate/positional token.
func namedFlags(args []string, wanted map[string]bool) bool {
	values := map[string]bool{"f": true, "p": true, "stack-name": true, "project-name": true, "engine": true, "engine-socket": true, "timeout": true, "contract": true, "format": true, "coordination-layout": true, "coordination-daemon-id": true, "coordination-state-dir": true, "coordination-source": true, "preflight-contract": true, "expect-preflight": true, "pull": true, "env-file": true, "secret-bindings": true, "secret-target": true, "proof-file": true, "proof-key-file": true}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			return false
		}
		if len(token) < 2 || token[0] != '-' {
			continue
		}
		name := strings.TrimPrefix(token, "-")
		name = strings.TrimPrefix(name, "-")
		key, _, equal := strings.Cut(name, "=")
		if wanted[key] {
			return true
		}
		if !equal && values[key] {
			i++
		}
	}
	return false
}
func namedBindingIntent(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return namedFlags(args[1:], map[string]bool{"preflight-contract": true, "expect-preflight": true})
}
func namedSecretIntent(args []string) bool {
	if _, ok := os.LookupEnv("DILL_SECRET_POLICY"); ok {
		return true
	}
	if len(args) == 0 {
		return false
	}
	return namedFlags(args[1:], map[string]bool{"secret-bindings": true, "secret-target": true, "proof-file": true, "proof-key-file": true})
}
