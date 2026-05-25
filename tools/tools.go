//go:build tools

package tools

// This file documents codegen tool dependencies.
// To regenerate internal/config structs from dill.pkl:
//  1. Install pkl:       https://pkl-lang.org
//  2. Install codegen:   go install github.com/apple/pkl-go/cmd/pkl-gen-go@latest
//  3. Run:               go generate ./internal/config/
