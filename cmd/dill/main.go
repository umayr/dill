package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

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
	verbosity, remaining := logger.ParseVerbosity(os.Args[1:])
	logger.Init(verbosity)

	err := run(remaining)
	if err != nil {
		var ue usageError
		switch {
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
	var ue usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}

func run(args []string) error {
	fs := flag.NewFlagSet("dill", flag.ContinueOnError)
	opts := commandOptions{}
	fs.StringVar(&opts.ConfigFile, "f", "compose.pkl", "path to the .pkl config file")
	fs.StringVar(&opts.StackName, "p", "", "stack name override")
	fs.StringVar(&opts.StackName, "stack-name", "", "stack name override")
	fs.StringVar(&opts.StackName, "project-name", "", "stack name override")
	fs.StringVar(&opts.Engine, "engine", "", "container engine override: podman or docker")
	fs.DurationVar(&opts.Timeout, "timeout", 5*time.Minute, "timeout for the entire stack to become ready")
	fs.StringVar(&opts.EnvFile, "env-file", "", "path to an env file to load before running")
	fs.BoolVar(&opts.ForceRecreate, "force-recreate", false, "force recreate containers even if nothing changed")
	fs.BoolVar(&opts.Quiet, "quiet", false, "suppress progress output")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "show what would happen without changing containers")
	fs.BoolVar(&opts.Yes, "yes", false, "assume yes for destructive prompts")
	fs.BoolVar(&opts.Yes, "y", false, "assume yes for destructive prompts")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: dill [flags] <command>

Commands:
  up        Start all services (default)
  down      Stop and remove all dill-managed services
  teardown  Remove containers, images, volumes, and network (--force to force-remove images)
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

	if opts.EnvFile != "" {
		if err := loadEnvFile(opts.EnvFile); err != nil {
			return fmt.Errorf("loading env file: %w", err)
		}
	}

	cmd := cmdUp
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}

	ctx := context.Background()

	switch cmd {
	case cmdUp:
		return runUp(ctx, opts, fs.Args()[1:])
	case cmdDown:
		return runDown(ctx, opts, fs.Args()[1:])
	case cmdTeardown:
		return runTeardown(ctx, opts, fs.Args()[1:])
	case cmdStop:
		return runStop(ctx, opts, fs.Args()[1:])
	case cmdStart:
		return runStart(ctx, opts, fs.Args()[1:])
	case cmdRestart:
		return runRestart(ctx, opts, fs.Args()[1:])
	case cmdPs:
		return runPs(ctx, opts, fs.Args()[1:])
	case cmdLogs:
		return runLogs(ctx, opts, fs.Args()[1:])
	case cmdPull:
		return runPull(ctx, opts, fs.Args()[1:])
	case cmdImages:
		return runImages(ctx, opts, fs.Args()[1:])
	case cmdVolumes:
		return runVolumes(ctx, opts, fs.Args()[1:])
	case cmdExec:
		return runExec(ctx, opts, fs.Args()[1:])
	case cmdPlan:
		return runPlan(ctx, opts, fs.Args()[1:])
	case cmdDiff:
		err := runDiff(ctx, opts, fs.Args()[1:])
		// diff is for humans and exits successfully when it renders drift.
		// plan keeps the CI-oriented non-zero exit for changes.
		if errors.Is(err, errPlanHasChanges) {
			return nil
		}
		return err
	case cmdStatus:
		return runStatus(ctx, opts, fs.Args()[1:])
	case cmdDoctor:
		return runDoctor(ctx, opts, fs.Args()[1:])
	case cmdStacks, cmdLs:
		return runStacks(ctx, opts, fs.Args()[1:])
	case cmdRm:
		return runRm(ctx, opts, fs.Args()[1:])
	case cmdWait:
		return runWait(ctx, opts, fs.Args()[1:])
	case cmdPort:
		return runPort(ctx, opts, fs.Args()[1:])
	case cmdTop:
		return runTop(ctx, opts, fs.Args()[1:])
	case cmdCp:
		return runCp(ctx, opts, fs.Args()[1:])
	case cmdConfig:
		return runConfig(ctx, opts, fs.Args()[1:])
	case cmdInit:
		return runInit(opts, fs.Args()[1:])
	case cmdValidate:
		return runValidate(ctx, opts, fs.Args()[1:])
	case cmdFmt:
		return runFmt(ctx, opts, fs.Args()[1:])
	case cmdVersion:
		if err := ensureNoArgs(fs.Args()[1:], "version"); err != nil {
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
