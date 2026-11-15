package preflight

import (
	"flag"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Invocation is a strictly parsed opt-in named-profile request, not a saved plan
// or authorization. It performs NO config/Pkl/engine/state/filesystem access.
// Config evaluation, host qualification, capture and comparison remain required.
type Invocation struct {
	Operation                          string
	ConfigFile, Stack, Engine, Socket  string
	Timeout                            time.Duration
	Quiet, ForceRecreate, NoPrune      bool
	Pull                               string
	Layout, DaemonID, StateDir, Source string
	ExpectedPreflight                  string
}

// ParseInvocation takes the operation name followed by flags (no namespace).
// apply selects dill.operation/1 plus both mandatory negotiated binding flags.
// The returned code is fixed/safe; never reflect flag/parser diagnostics.
func ParseInvocation(args []string, apply bool) (Invocation, string) {
	v := Invocation{Timeout: 5 * time.Minute}
	if len(args) == 0 || !SupportedOperation(args[0]) {
		return v, "UNSUPPORTED_OPERATION"
	}
	v.Operation = args[0]
	fs := flag.NewFlagSet("named-preflight", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&v.ConfigFile, "f", "", "")
	for _, name := range []string{"p", "stack-name", "project-name"} {
		fs.StringVar(&v.Stack, name, "", "")
	}
	fs.StringVar(&v.Engine, "engine", "", "")
	fs.StringVar(&v.Socket, "engine-socket", "", "")
	fs.DurationVar(&v.Timeout, "timeout", 5*time.Minute, "")
	fs.BoolVar(&v.Quiet, "quiet", false, "")
	contract, format := "", "json"
	fs.StringVar(&contract, "contract", "", "")
	fs.StringVar(&format, "format", "json", "")
	fs.StringVar(&v.Layout, "coordination-layout", "", "")
	fs.StringVar(&v.DaemonID, "coordination-daemon-id", "", "")
	fs.StringVar(&v.StateDir, "coordination-state-dir", "", "")
	fs.StringVar(&v.Source, "coordination-source", "", "")
	if v.Operation == "up" {
		fs.BoolVar(&v.ForceRecreate, "force-recreate", false, "")
		fs.BoolVar(&v.NoPrune, "no-prune", false, "")
		fs.StringVar(&v.Pull, "pull", "", "")
	}
	bindingContract := ""
	if apply {
		fs.StringVar(&bindingContract, "preflight-contract", "", "")
		fs.StringVar(&v.ExpectedPreflight, "expect-preflight", "", "")
	}
	seen, ok := uniqueFlags(fs, args[1:])
	if !ok || fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
		return v, "INVALID_ARGUMENTS"
	}
	wantContract := Version
	if apply {
		wantContract = "dill.operation/1"
	}
	if contract != wantContract {
		return v, "UNSUPPORTED_CONTRACT"
	}
	if format != "json" {
		return v, "UNSUPPORTED_FORMAT"
	}
	if apply && (!seen["preflight-contract"] || !seen["expect-preflight"] || bindingContract != Version || !ValidDigest(v.ExpectedPreflight)) {
		return v, "INVALID_ARGUMENTS"
	}
	for _, name := range []string{"f", "stack", "engine", "engine-socket", "coordination-layout", "coordination-daemon-id", "coordination-state-dir", "coordination-source"} {
		if !seen[name] {
			return v, "INVALID_TARGET"
		}
	}
	if v.ConfigFile == "" || !ValidName(v.Stack) || v.Engine != "docker" || !explicitUnixSocket(v.Socket) || v.DaemonID == "" || !filepath.IsAbs(v.StateDir) {
		return v, "INVALID_TARGET"
	}
	if v.Timeout <= 0 || v.Layout != "native-local-docker-v1" || (v.Source != "cli" && v.Source != "brine") {
		return v, "UNSUPPORTED_PREFLIGHT"
	}
	if v.Pull != "" && v.Pull != "never" {
		return v, "UNSUPPORTED_PREFLIGHT"
	}
	return v, ""
}
func explicitUnixSocket(value string) bool {
	// Match the existing explicit localEngineSocket grammar without ambient
	// lookup, filesystem probes, cleaning or normalizing a different target.
	if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) || strings.ContainsAny(value, "?#%\\") {
		return false
	}
	path := value
	if !strings.HasPrefix(value, "/") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		path = u.Path
		if value != "unix://"+path {
			return false
		}
	}
	return strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && strings.TrimRightFunc(path, unicode.IsSpace) == path
}
func uniqueFlags(fs *flag.FlagSet, args []string) (map[string]bool, bool) {
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || len(arg) < 2 || arg[0] != '-' {
			return nil, false
		}
		name := arg[1:]
		if strings.HasPrefix(name, "-") {
			name = name[1:]
		}
		if name == "" || strings.HasPrefix(name, "-") {
			return nil, false
		}
		name, _, equal := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			return nil, false
		}
		key := name
		if name == "p" || name == "stack-name" || name == "project-name" {
			key = "stack"
		}
		if seen[key] {
			return nil, false
		}
		seen[key] = true
		boolean := false
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
			boolean = b.IsBoolFlag()
		}
		// Like Go flag, string values may begin '-' or contain flag-looking
		// substrings. Consume them as values, not another authority-bearing flag.
		if !equal && !boolean {
			i++
			if i >= len(args) {
				return nil, false
			}
		}
	}
	return seen, true
}
