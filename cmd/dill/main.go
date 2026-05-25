package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/loader"
)

// starterTemplate is written by `dill init`. The amends path should point to
// dill.pkl relative to the compose file; adjust it for your directory layout.
const starterTemplate = `amends "../../dill.pkl"

config {
  engine = "podman"
  services {
    ["web"] {
      image = "nginx:alpine"
      ports = List("8080:80")
    }
  }
}
`

func main() {
	verbosity, remaining := logger.ParseVerbosity(os.Args[1:])
	logger.Init(verbosity)

	if err := run(remaining); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("dill", flag.ExitOnError)
	configFile := fs.String("f", "compose.pkl", "path to the .pkl config file")
	timeout := fs.Duration("timeout", 60*time.Second, "timeout for each service to become ready")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: dill [flags] <command>

Commands:
  up        Start all services (default)
  down      Stop and remove all dill-managed services
  stop      Stop services without removing containers
  start     Start previously stopped containers
  restart   Restart services
  ps        List containers in the stack
  logs      Fetch container logs
  pull      Pull service images without starting
  images    List images used by the config
  exec      Execute a command in a running container
  init      Create a starter compose.pkl in the current directory
  validate  Check a .pkl config file for errors
  fmt       Format a .pkl config file in-place

Flags:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  -v/-vv/-vvv/-vvvv\n    \tVerbosity level\n")
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	cmd := "up"
	if fs.NArg() > 0 {
		cmd = fs.Arg(0)
	}

	ctx := context.Background()

	switch cmd {
	case "up":
		return runUp(ctx, *configFile, *timeout)
	case "down":
		return runDown(ctx, *configFile)
	case "stop":
		return runStop(ctx, *configFile)
	case "start":
		return runStart(ctx, *configFile)
	case "restart":
		return runRestart(ctx, *configFile)
	case "ps":
		return runPs(ctx, *configFile)
	case "logs":
		return runLogs(ctx, *configFile, fs.Args()[1:])
	case "pull":
		return runPull(ctx, *configFile)
	case "images":
		return runImages(ctx, *configFile)
	case "exec":
		return runExec(ctx, *configFile, fs.Args()[1:])
	case "init":
		return runInit(*configFile)
	case "validate":
		return runValidate(ctx, *configFile)
	case "fmt":
		return runFmt(ctx, *configFile)
	default:
		return fmt.Errorf("unknown command %q (run dill --help for usage)", cmd)
	}
}

// --- up ---

func runUp(ctx context.Context, configFile string, timeout time.Duration) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Info("loaded config", "engine", cfg.Engine, "services", len(cfg.Services))

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	g, err := dag.Build(cfg.Services)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	stackName := stackNameFrom(configFile)
	logger.Debug("using stack name", "stack", stackName)

	return g.Run(ctx, engine, stackName, timeout)
}

// --- down ---

func runDown(ctx context.Context, configFile string) error {
	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()
	return dag.Down(ctx, engine, stackName)
}

// --- stop ---

func runStop(ctx context.Context, configFile string) error {
	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	if len(names) == 0 {
		logger.Info("no containers found", "stack", stackName)
		return nil
	}
	for _, name := range names {
		if err := engine.StopService(ctx, name); err != nil {
			logger.Warn("stop failed", "name", name, "err", err)
		}
	}
	return nil
}

// --- start ---

func runStart(ctx context.Context, configFile string) error {
	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	if len(names) == 0 {
		logger.Info("no containers found", "stack", stackName)
		return nil
	}
	for _, name := range names {
		logger.Info("starting container", "name", name)
		if err := engine.StartExisting(ctx, name); err != nil {
			logger.Warn("start failed", "name", name, "err", err)
		}
	}
	return nil
}

// --- restart ---

func runRestart(ctx context.Context, configFile string) error {
	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	if len(names) == 0 {
		logger.Info("no containers found", "stack", stackName)
		return nil
	}
	for _, name := range names {
		logger.Info("restarting", "name", name)
		if err := engine.StopService(ctx, name); err != nil {
			logger.Warn("stop failed", "name", name, "err", err)
		}
		if err := engine.StartExisting(ctx, name); err != nil {
			logger.Warn("start failed", "name", name, "err", err)
		}
	}
	return nil
}

// --- ps ---

func runPs(ctx context.Context, configFile string) error {
	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSTATE\tSTATUS\tIMAGE\tPORTS")
	for _, name := range names {
		cs, err := engine.ServiceStatus(ctx, name)
		if err != nil {
			fmt.Fprintf(w, "%s\t-\t-\terror: %s\t-\t-\n", name, err)
			continue
		}
		ports := strings.Join(cs.Ports, ", ")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			cs.Name, cs.ID, cs.State, cs.Status, cs.Image, ports)
	}
	return w.Flush()
}

// --- logs ---

func runLogs(ctx context.Context, configFile string, args []string) error {
	lfs := flag.NewFlagSet("logs", flag.ExitOnError)
	follow := lfs.Bool("f", false, "follow log output")
	tail := lfs.Int("n", 0, "number of lines to show from the end (0 = all)")
	if err := lfs.Parse(args); err != nil {
		return err
	}

	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	// If a service name is provided, show only that container.
	var names []string
	if lfs.NArg() > 0 {
		svc := lfs.Arg(0)
		// Resolve to container name: try direct name first, then stack-prefixed.
		names = []string{svc}
		// Check if the container exists; fall back to stack_svc naming.
		if _, err := engine.ServiceStatus(ctx, svc); err != nil {
			names = []string{stackName + "_" + svc}
		}
	} else {
		names, err = engine.ListStack(ctx, stackName)
		if err != nil {
			return fmt.Errorf("listing stack: %w", err)
		}
	}

	if len(names) == 0 {
		logger.Info("no containers found", "stack", stackName)
		return nil
	}

	// For a single container, stream directly. For multiple, prefix each line.
	// Simple approach: stream sequentially (follow only makes sense for one).
	if len(names) == 1 || *follow {
		name := names[0]
		return engine.Logs(ctx, name, *follow, *tail, os.Stdout)
	}
	for _, name := range names {
		fmt.Printf("=== %s ===\n", name)
		if err := engine.Logs(ctx, name, false, *tail, os.Stdout); err != nil {
			logger.Warn("logs failed", "name", name, "err", err)
		}
	}
	return nil
}

// --- pull ---

func runPull(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	for name, svc := range cfg.Services {
		logger.Info("pulling", "service", name, "image", svc.Image)
		if err := engine.PullImage(ctx, svc.Image); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
	}
	return nil
}

// --- images ---

func runImages(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tIMAGE")
	for name, svc := range cfg.Services {
		fmt.Fprintf(w, "%s\t%s\n", name, svc.Image)
	}
	return w.Flush()
}

// --- exec ---

func runExec(ctx context.Context, configFile string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: dill exec <service> [command...]")
	}
	svc := args[0]
	cmdArgs := args[1:]
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"sh"}
	}

	stackName := stackNameFrom(configFile)
	engine, err := detectEngine(ctx)
	if err != nil {
		return err
	}
	defer engine.Close()

	// Resolve container name.
	containerName := svc
	if _, err := engine.ServiceStatus(ctx, svc); err != nil {
		containerName = stackName + "_" + svc
	}

	// Determine which binary to use based on the engine type.
	binary := engineBinary(engine)
	execArgs := append([]string{"exec", "-it", containerName}, cmdArgs...)
	c := exec.CommandContext(ctx, binary, execArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// --- init ---

func runInit(configFile string) error {
	if _, err := os.Stat(configFile); err == nil {
		return fmt.Errorf("%s already exists", configFile)
	}
	if err := os.WriteFile(configFile, []byte(starterTemplate), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", configFile, err)
	}
	fmt.Printf("created %s\n", configFile)
	return nil
}

// --- validate ---

func runValidate(ctx context.Context, configFile string) error {
	if err := config.Validate(ctx, configFile); err != nil {
		return err
	}
	fmt.Printf("%s is valid\n", configFile)
	return nil
}

// --- fmt ---

func runFmt(ctx context.Context, configFile string) error {
	bin, err := loader.FindPkl(ctx)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "format", "-w", configFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pkl format: %w", err)
	}
	fmt.Printf("formatted %s\n", configFile)
	return nil
}

// --- helpers ---

func newEngine(ctx context.Context, engineName string) (orchestrator.Engine, error) {
	switch strings.ToLower(engineName) {
	case "docker":
		return orchestrator.NewDockerEngine()
	case "podman", "":
		return newPodmanEngine(ctx)
	default:
		return nil, fmt.Errorf("unknown engine %q (supported: podman, docker)", engineName)
	}
}

// detectEngine tries podman first, then docker. Used by commands that don't load config.
func detectEngine(ctx context.Context) (orchestrator.Engine, error) {
	engine, err := newPodmanEngine(ctx)
	if err != nil {
		logger.Debug("podman unavailable, trying docker", "err", err)
		engine2, err2 := orchestrator.NewDockerEngine()
		if err2 != nil {
			return nil, fmt.Errorf("no container engine available: %w", err2)
		}
		return engine2, nil
	}
	return engine, nil
}

func newPodmanEngine(ctx context.Context) (orchestrator.Engine, error) {
	socket := os.Getenv("PODMAN_SOCKET")
	if socket == "" {
		socket = podmanSocket()
	}
	logger.Debug("connecting to podman", "socket", socket)
	return orchestrator.NewPodmanEngine(ctx, socket)
}

// podmanSocket is defined per-platform in socket_darwin.go / socket_linux.go / socket_windows.go.

func stackNameFrom(path string) string {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if name == "" {
		return "dill"
	}
	return name
}

func engineBinary(e orchestrator.Engine) string {
	switch e.(type) {
	case *orchestrator.DockerEngine:
		return "docker"
	default:
		return "podman"
	}
}

