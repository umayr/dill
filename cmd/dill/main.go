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

	if err := run(remaining); err != nil {
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintln(os.Stderr, "dill: "+ue.msg)
		} else {
			logger.Error("fatal", "err", err)
		}
		os.Exit(2)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("dill", flag.ExitOnError)
	configFile := fs.String("f", "compose.pkl", "path to the .pkl config file")
	timeout := fs.Duration("timeout", 5*time.Minute, "timeout for the entire stack to become ready")
	envFile := fs.String("env-file", "", "path to an env file to load before running")
	forceRecreate := fs.Bool("force-recreate", false, "force recreate containers even if nothing changed")
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
  exec      Execute a command in a running container
  plan      Show what would change if you ran up
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
		return err
	}

	if *envFile != "" {
		if err := loadEnvFile(*envFile); err != nil {
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
		return runUp(ctx, *configFile, *timeout, *forceRecreate, fs.Args()[1:])
	case cmdDown:
		return runDown(ctx, *configFile, fs.Args()[1:])
	case cmdTeardown:
		return runTeardown(ctx, *configFile, fs.Args()[1:])
	case cmdStop:
		return runStop(ctx, *configFile, fs.Args()[1:])
	case cmdStart:
		return runStart(ctx, *configFile, *timeout, fs.Args()[1:])
	case cmdRestart:
		return runRestart(ctx, *configFile, *timeout, fs.Args()[1:])
	case cmdPs:
		return runPs(ctx, *configFile, fs.Args()[1:])
	case cmdLogs:
		return runLogs(ctx, *configFile, fs.Args()[1:])
	case cmdPull:
		return runPull(ctx, *configFile)
	case cmdImages:
		return runImages(ctx, *configFile)
	case cmdExec:
		return runExec(ctx, *configFile, fs.Args()[1:])
	case cmdPlan:
		err := runPlan(ctx, *configFile, fs.Args()[1:])
		if errors.Is(err, errPlanHasChanges) {
			os.Exit(1)
		}
		return err
	case cmdConfig:
		return runConfig(ctx, *configFile, fs.Args()[1:])
	case cmdInit:
		return runInit(*configFile)
	case cmdValidate:
		return runValidate(ctx, *configFile, fs.Args()[1:])
	case cmdFmt:
		return runFmt(ctx, *configFile, fs.Args()[1:])
	case cmdVersion:
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
