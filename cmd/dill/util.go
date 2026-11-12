package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/orchestrator"
)

type commandOptions struct {
	secretDiagnostics                                    *secretDiagnostics
	SecretBindings, SecretPolicy, SecretTarget           string
	SecretBindingsSet, SafeSecrets, SecretPolicyCaptured bool
	StackNameSet                                         bool
	ConfigFileSet                                        bool
	ProofKeyFile, ProofFile                              string
	ProofKeyFileSet, ProofFileSet                        bool
	ConfigFile                                           string
	Timeout                                              time.Duration
	EnvFile                                              string
	Engine                                               string
	EngineSocket                                         string
	EngineSocketSet                                      bool
	StackName                                            string
	Quiet                                                bool
	DryRun                                               bool
	Yes                                                  bool
	ForceRecreate                                        bool
}

// printProgress writes a clean, fixed-width progress line to stdout.
func printProgress(service, status string) {
	fmt.Printf("  %-14s %s\n", status, service)
}

func (o commandOptions) progress() dag.Progress {
	if o.Quiet {
		return nil
	}
	return printProgress
}

func (o commandOptions) downProgress(stackName string) dag.Progress {
	if o.Quiet {
		return nil
	}
	return downProgress(stackName)
}

// addFileFlag registers the -f config-file flag for commands that only need
// the path (init, fmt) and do not resolve a full config.
func addFileFlag(fs *flag.FlagSet, opts *commandOptions) {
	addConfigFileFlag(fs, opts)
}

// addEngineFlag registers the --engine override used to select which container
// runtime a command operates against.
func addEngineFlag(fs *flag.FlagSet, opts *commandOptions) {
	fs.StringVar(&opts.Engine, "engine", opts.Engine, "container engine override: podman or docker")
}

// addEngineSocketFlag preserves an explicitly empty value so it cannot silently
// fall back to an ambient endpoint.
func addEngineSocketFlag(fs *flag.FlagSet, opts *commandOptions) {
	fs.Func("engine-socket", "explicit local engine socket: absolute path or unix:///path", func(value string) error {
		opts.EngineSocket, opts.EngineSocketSet = value, true
		return nil
	})
}

func addConfigFileFlag(fs *flag.FlagSet, opts *commandOptions) {
	fs.Func("f", "path to the .pkl config file", func(v string) error { opts.ConfigFile = v; opts.ConfigFileSet = true; return nil })
}

// addConfigFlags registers the flags shared by every command that resolves a
// config file: the path itself, the stack/project name, the engine override,
// and an optional env file.
func addConfigFlags(fs *flag.FlagSet, opts *commandOptions) {
	captureSecretPolicy(opts)
	if opts.secretMode() {
		fs.SetOutput(io.Discard)
	}
	addSecretBindingsFlag(fs, opts)
	fs.StringVar(&opts.SecretTarget, "secret-target", opts.SecretTarget, "trusted secret target ID")
	addFileFlag(fs, opts)
	addStackFlags(fs, opts)
	addEngineFlag(fs, opts)
	addEngineSocketFlag(fs, opts)
	fs.StringVar(&opts.EnvFile, "env-file", opts.EnvFile, "path to an env file to load before running")
}

// addActionFlags registers the flags shared by commands that mutate state:
// progress suppression, dry-run preview, and destructive-confirmation override.
func addActionFlags(fs *flag.FlagSet, opts *commandOptions) {
	fs.BoolVar(&opts.Quiet, "quiet", opts.Quiet, "suppress progress output")
	fs.BoolVar(&opts.DryRun, "dry-run", opts.DryRun, "show what would happen without changing containers")
	fs.BoolVar(&opts.Yes, "yes", opts.Yes, "assume yes for destructive prompts")
	fs.BoolVar(&opts.Yes, "y", opts.Yes, "assume yes for destructive prompts")
}

// addTimeoutFlag registers the --timeout flag for commands that wait on
// container readiness (up, start, restart, wait).
func addTimeoutFlag(fs *flag.FlagSet, opts *commandOptions) {
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "timeout for the entire stack to become ready")
}

func confirmDestructive(action string, opts commandOptions) (bool, error) {
	if opts.Yes {
		return true, nil
	}
	fmt.Fprintf(os.Stderr, "%s. Continue? [y/N] ", action)
	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && len(answer) == 0 {
		return false, err
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}

// downProgress returns a Progress func that strips the stack name prefix
// from container names before printing (e.g. "myapp_web" → "web").
func downProgress(stackName string) dag.Progress {
	return func(name, status string) {
		display := strings.TrimPrefix(name, stackName+"_")
		printProgress(display, status)
	}
}

var stackAdjectives = []string{
	"autumn", "bold", "brave", "bright", "calm", "clever", "cold", "cool",
	"crisp", "dark", "deft", "eager", "early", "faint", "fast", "firm",
	"fleet", "glad", "golden", "grand", "happy", "hardy", "heavy", "hidden",
	"icy", "idle", "keen", "kind", "lofty", "loud", "lucky", "merry",
	"mild", "misty", "neat", "noble", "odd", "pale", "plain", "proud",
	"quick", "quiet", "rapid", "rare", "rough", "round", "royal", "rusty",
	"sandy", "sharp", "shiny", "silent", "silver", "slim", "smart", "snowy",
	"soft", "solid", "speedy", "stout", "strong", "sunny", "swift", "tall",
	"tame", "tiny", "tough", "true", "vast", "warm", "wild", "wise",
}

var stackAnimals = []string{
	"ant", "ape", "bear", "bee", "bird", "boar", "buck", "bull",
	"cat", "clam", "crab", "crow", "deer", "dog", "dove", "duck",
	"eagle", "elk", "fish", "flea", "fly", "frog", "gnu", "goat",
	"hawk", "hen", "hog", "ibis", "jay", "kite", "lamb", "lark",
	"lion", "lynx", "mink", "mole", "moth", "mule", "newt", "owl",
	"ox", "pike", "pony", "puma", "quail", "ram", "rat", "rook",
	"seal", "shrew", "slug", "snail", "snake", "stag", "swan", "toad",
	"trout", "vole", "wasp", "wolf", "worm", "wren", "yak", "zebra",
}

// resolveStackName returns cfg.Name when set, otherwise derives a deterministic
// adjective-animal name from the absolute path of the config file.
func resolveStackName(cfg *config.DillConfig, configFile string) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return derivedStackName(configFile)
}

func resolveStackNameWithOptions(cfg *config.DillConfig, opts commandOptions) string {
	if opts.StackName != "" {
		return opts.StackName
	}
	return resolveStackName(cfg, opts.ConfigFile)
}

func loadConfig(ctx context.Context, opts commandOptions) (result *config.DillConfig, retErr error) {
	captureSecretPolicy(&opts)
	discovered := false
	defer func() {
		if opts.secretMode() || discovered || hasSecretGrants(result) {
			retErr = fixedSecretError(retErr)
		}
	}()

	if opts.EnvFile != "" {
		if err := loadEnvFile(opts.EnvFile); err != nil {
			return nil, fmt.Errorf("loading env file: %w", err)
		}
	}
	if opts.secretMode() {
		ctx = config.WithPrivateEvaluation(ctx)
	}
	cfg, err := configLoader(ctx, opts.ConfigFile)
	result = cfg
	discovered = hasSecretGrants(cfg)
	if discovered {
		opts.secretDiagnostics.enable()
	}
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if err := validateTrustedStack(opts, cfg); err != nil {
		return nil, err
	}
	if opts.Engine != "" {
		cfg.Engine = opts.Engine
	}
	if err := config.ValidateContainerNames(cfg.Services, resolveStackNameWithOptions(cfg, opts)); err != nil {
		return nil, &config.InvalidError{Err: err}
	}
	return cfg, nil
}

var configLoader = config.Load

func openEngine(ctx context.Context, cfg *config.DillConfig, opts commandOptions) (orchestrator.Engine, error) {
	engine, err := newEngine(ctx, cfg.Engine, opts)
	if err != nil {
		return nil, err
	}
	return engine, nil
}

func ensureNoArgs(args []string, usage string) error {
	if len(args) > 0 {
		return usageError{fmt.Sprintf("%s: unexpected argument %q", usage, args[0])}
	}
	return nil
}

func validateFormat(format string, allowed ...string) error {
	for _, a := range allowed {
		if format == a {
			return nil
		}
	}
	return usageError{fmt.Sprintf("unknown format %q (supported: %s)", format, strings.Join(allowed, ", "))}
}

func selectServices(all map[string]*config.Service, names []string, includeDeps bool) (map[string]*config.Service, error) {
	if len(names) == 0 {
		return all, nil
	}
	for _, name := range names {
		if _, ok := all[name]; !ok {
			return nil, usageError{fmt.Sprintf("unknown service %q", name)}
		}
	}
	if includeDeps {
		return filterServices(all, names), nil
	}
	out := make(map[string]*config.Service, len(names))
	for _, name := range names {
		out[name] = all[name]
	}
	return out, nil
}

func serviceContainerName(stackName, service string, svc *config.Service) string {
	if svc != nil && svc.ContainerName != "" {
		return svc.ContainerName
	}
	return stackName + "_" + service
}

func derivedStackName(configFile string) string {
	abs, err := filepath.Abs(configFile)
	if err != nil {
		abs = configFile
	}
	h := fnv.New32a()
	h.Write([]byte(abs))
	sum := h.Sum32()
	adj := stackAdjectives[sum%uint32(len(stackAdjectives))]
	ani := stackAnimals[(sum/uint32(len(stackAdjectives)))%uint32(len(stackAnimals))]
	return adj + "-" + ani
}

// loadEnvFile reads KEY=VALUE pairs from path and sets them as environment
// variables, skipping keys that are already set. Blank lines and lines
// beginning with # are ignored. Surrounding single or double quotes are
// stripped from values.
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening env file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func addStackFlags(fs *flag.FlagSet, o *commandOptions) {
	for _, name := range []string{"p", "stack-name", "project-name"} {
		fs.Func(name, "stack name override", func(v string) error { o.StackName = v; o.StackNameSet = true; return nil })
	}
}
