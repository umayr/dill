package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/log"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// starterTemplate is written by `dill init`.
const starterTemplate = `amends "../../dill.pkl"

config {
  name   = "myapp"
  engine = "podman"
  services {
    ["web"] {
      image = "nginx:alpine"
      ports = List("8080:80")
    }
  }
}
`

// usageError signals a user mistake (unknown command, bad flag) rather than a
// system failure. main() prints it plainly to stderr without the ERRO prefix.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func main() {
	if isProtocolInvocation(os.Args[1:]) && !secretInvocation(os.Args[1:]) {
		os.Exit(runProtocol(os.Args[1:]))
	}
	verbosity, remaining := logger.ParseVerbosity(os.Args[1:])
	logger.Init(verbosity)

	err := run(remaining)
	if err != nil {
		var ue usageError
		var pe protocolExit
		switch {
		case errors.Is(err, config.ErrSecretUnavailable):
			fmt.Fprintln(os.Stderr, config.ErrSecretUnavailable.Error())
		case errors.As(err, &pe):
			// Protocol output is already complete or undeliverable; no prose.
		case errors.Is(err, errPlanHasChanges) || errors.Is(err, errDoctorFoundIssues):
			// The command already rendered its result. The non-zero exit code
			// is the machine-readable signal (plan drift / failing checks).
		case errors.As(err, &ue):
			fmt.Fprintln(os.Stderr, "dill: "+ue.msg)
		default:
			logger.Error("fatal", "err", err)
		}
	}
	os.Exit(exitCodeFor(err))
}

// exitCodeFor returns the process exit code for a run() result: 0 for success,
// 1 for an operational failure or detected drift/issues, and 2 for a usage
// error (unknown command, bad flag, unknown service, and similar user errors).
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var pe protocolExit
	if errors.As(err, &pe) {
		return pe.code
	}
	var ue usageError
	var invalid *config.InvalidError
	if errors.As(err, &ue) || errors.As(err, &invalid) {
		return 2
	}
	return 1
}

func run(args []string) (retErr error) {
	// The executable operation namespace has its own fixed diagnostics and
	// negotiation. Keep it outside legacy secret/protocol result rewriting.
	if len(args) > 0 && args[0] == cmdOperation {
		return runOperation(context.Background(), args[1:])
	}
	if len(args) > 0 && args[0] == cmdOperationCapabilities {
		return runOperationCapabilities(args[1:])
	}
	if len(args) > 0 && args[0] == "coordination" {
		return runCoordination(context.Background(), args[1:])
	}
	safe := secretInvocation(args)
	check := secretCheckInvocation(args)
	diagnostics := &secretDiagnostics{}
	if safe {
		diagnostics.enable()
	}
	defer diagnostics.close()
	defer func() {
		if diagnostics.safe {
			retErr = fixedSecretError(retErr)
		}
		if check {
			if retErr != nil {
				fmt.Fprintln(os.Stdout, secretUnavailableJSON)
				retErr = protocolExit{1}
			} else {
				fmt.Fprintln(os.Stdout, secretAvailableJSON)
			}
		}
	}()

	if isProtocolInvocation(args) {
		if safe {
			return config.ErrSecretUnavailable
		}
		if code := runProtocol(args); code != 0 {
			return protocolExit{code}
		}
		return nil
	}
	fs := flag.NewFlagSet("dill", flag.ContinueOnError)
	opts := commandOptions{SafeSecrets: safe, secretDiagnostics: diagnostics}
	captureSecretPolicy(&opts)
	if safe {
		fs.SetOutput(io.Discard)
	}
	addSecretBindingsFlag(fs, &opts)
	addProofFlags(fs, &opts)
	fs.StringVar(&opts.SecretTarget, "secret-target", "", "trusted secret target ID")
	opts.ConfigFile = "compose.pkl"
	addConfigFileFlag(fs, &opts)
	addStackFlags(fs, &opts)
	fs.StringVar(&opts.Engine, "engine", "", "container engine override: podman or docker")
	addEngineSocketFlag(fs, &opts)
	fs.DurationVar(&opts.Timeout, "timeout", 5*time.Minute, "timeout for the entire stack to become ready")
	fs.StringVar(&opts.EnvFile, "env-file", "", "path to an env file to load before running")
	fs.BoolVar(&opts.ForceRecreate, "force-recreate", false, "force recreate containers even if nothing changed")
	fs.BoolVar(&opts.Quiet, "quiet", false, "suppress progress output")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show what would happen without changing containers")
	fs.BoolVar(&opts.Yes, "yes", false, "assume yes for destructive prompts")
	fs.BoolVar(&opts.Yes, "y", false, "assume yes for destructive prompts")
	fs.Usage = func() {
		if safe {
			return
		}
		fmt.Fprintf(os.Stderr, `Usage: dill [flags] <command>

Commands:
  up        Start all services (default)
  down      Stop and remove services; retain networks
  teardown  Remove containers and images; retain volumes and networks
  stop      Stop services without removing containers
  start     Start previously stopped containers
  restart   Restart services
  ps        List containers in the stack
  logs      Fetch container logs
  pull      Pull service images without starting
  images    List images used by the config
  volumes   List stack-owned volumes
  exec      Execute a command in a running container
  plan      Show what would change if you ran up
  diff      Show desired vs live changes
  status    Show stack status
  stacks    List dill-managed stacks (alias: ls)
  doctor    Check local dill, Pkl, and engine readiness
  caps      Show known adapter limitations as JSON
  secret-capabilities  Discover the Alpha file-secret contract as JSON
  secrets check        Check authorized local secret sources without mutation
  operation-capabilities  Discover the bounded local operation contract
  operation Execute an attended local operation (--contract dill.operation/1)
  coordination Inspect/acknowledge qualified local crash state (offline)
  protocol  Discover the experimental metadata-only operation protocol
  rm        Remove stopped containers in the stack
  wait      Wait for services to become ready
  port      Print published ports for a service
  top       Show processes in a running container
  cp        Copy files between a container and the host
  config    Print the resolved config (--format json|yaml)
  init      Create a starter compose.pkl in the current directory
  validate  Check a .pkl config file for errors
  fmt       Format a .pkl config file in-place
  version   Print the dill version

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  -v/-vv/-vvv/-vvvv\n    \tVerbosity level\n")
	}

	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}

	cmd := cmdUp
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}

	// These legacy commands do not implement the Alpha diagnostic contract.
	// Reject before any local parser, env-file, evaluator or SDK access.
	if safe {
		switch cmd {
		case cmdInit, cmdFmt, cmdDoctor, cmdStacks, cmdLs:
			return config.ErrSecretUnavailable
		}
	}

	if cmd != cmdSecretCapabilities {
		if opts.EnvFile != "" {
			if err := loadEnvFile(opts.EnvFile); err != nil {
				return fmt.Errorf("loading env file: %w", err)
			}
		}

	}
	commandArgs := fs.Args()
	if len(commandArgs) > 0 {
		commandArgs = commandArgs[1:]
	}

	if opts.proofRequested() && cmd != cmdPlan {
		return config.ErrSecretUnavailable
	}
	ctx := context.Background()

	switch cmd {
	case cmdSecretCapabilities:
		return runSecretCapabilities(commandArgs)
	case cmdSecrets:
		return runSecretsCheck(ctx, opts, commandArgs)
	case cmdUp:
		return runUp(ctx, opts, commandArgs)
	case cmdDown:
		return runDown(ctx, opts, commandArgs)
	case cmdTeardown:
		return runTeardown(ctx, opts, commandArgs)
	case cmdStop:
		return runStop(ctx, opts, commandArgs)
	case cmdStart:
		return runStart(ctx, opts, commandArgs)
	case cmdRestart:
		return runRestart(ctx, opts, commandArgs)
	case cmdPs:
		return runPs(ctx, opts, commandArgs)
	case cmdLogs:
		return runLogs(ctx, opts, commandArgs)
	case cmdPull:
		return runPull(ctx, opts, commandArgs)
	case cmdImages:
		return runImages(ctx, opts, commandArgs)
	case cmdVolumes:
		return runVolumes(ctx, opts, commandArgs)
	case cmdExec:
		return runExec(ctx, opts, commandArgs)
	case cmdPlan:
		return runPlan(ctx, opts, commandArgs)
	case cmdDiff:
		err := runDiff(ctx, opts, commandArgs)
		// diff is for humans and exits successfully when it renders drift.
		// plan keeps the CI-oriented non-zero exit for changes.
		if errors.Is(err, errPlanHasChanges) {
			return nil
		}
		return err
	case cmdStatus:
		return runStatus(ctx, opts, commandArgs)
	case cmdDoctor:
		return runDoctor(ctx, opts, commandArgs)
	case cmdCaps:
		return runCaps(opts, commandArgs)
	case cmdStacks, cmdLs:
		return runStacks(ctx, opts, commandArgs)
	case cmdRm:
		return runRm(ctx, opts, commandArgs)
	case cmdWait:
		return runWait(ctx, opts, commandArgs)
	case cmdPort:
		return runPort(ctx, opts, commandArgs)
	case cmdTop:
		return runTop(ctx, opts, commandArgs)
	case cmdCp:
		return runCp(ctx, opts, commandArgs)
	case cmdConfig:
		return runConfig(ctx, opts, commandArgs)
	case cmdInit:
		return runInit(opts, commandArgs)
	case cmdValidate:
		return runValidate(ctx, opts, commandArgs)
	case cmdFmt:
		return runFmt(ctx, opts, commandArgs)
	case cmdVersion:
		if err := ensureNoArgs(commandArgs, "version"); err != nil {
			return err
		}
		fmt.Println(version)
		return nil
	default:
		msg := fmt.Sprintf("unknown command %q", cmd)
		if s := closestCommand(cmd); s != "" {
			msg += fmt.Sprintf(", did you mean %q?", s)
		} else {
			msg += " (run 'dill --help' for usage)"
		}
		return usageError{msg}
	}
}
