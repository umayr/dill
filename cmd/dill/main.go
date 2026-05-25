package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/loader"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
)

// version is set at build time via -ldflags "-X main.version=<tag>".
var version = "dev"

// errPlanHasChanges is returned by runPlan when drift is detected.
// The caller exits with code 1 without printing an error message —
// the rendered plan output already communicates the situation.
var errPlanHasChanges = errors.New("plan has changes")

// starterTemplate is written by `dill init`. The amends path should point to
// dill.pkl relative to the compose file; adjust it for your directory layout.
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
	timeout := fs.Duration("timeout", 5*time.Minute, "timeout for the entire stack to become ready")
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
	case "teardown":
		return runTeardown(ctx, *configFile, fs.Args()[1:])
	case "stop":
		return runStop(ctx, *configFile)
	case "start":
		return runStart(ctx, *configFile, *timeout)
	case "restart":
		return runRestart(ctx, *configFile, *timeout)
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
	case "plan":
		err := runPlan(ctx, *configFile)
		if errors.Is(err, errPlanHasChanges) {
			os.Exit(1) // defers have already run; exit without printing an error
		}
		return err
	case "init":
		return runInit(*configFile)
	case "validate":
		return runValidate(ctx, *configFile)
	case "fmt":
		return runFmt(ctx, *configFile)
	case "version":
		fmt.Println(version)
		return nil
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

	logger.Debug("loaded config", "engine", cfg.Engine, "services", len(cfg.Services))

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	stackName := resolveStackName(cfg, configFile)
	logger.Debug("using stack name", "stack", stackName)

	// Compute plan to determine what each service needs.
	p, err := plan.Compute(ctx, cfg, engine, stackName)
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	// Remove orphaned containers first (live but not in config).
	for _, ch := range p.Changes {
		if ch.Kind != plan.KindRemove {
			continue
		}
		display := strings.TrimPrefix(ch.Service, stackName+"_")
		printProgress(display, "removing orphan")
		if err := engine.StopService(ctx, ch.Service); err != nil {
			logger.Debug("stop orphan failed", "container", ch.Service, "err", err)
		}
		if err := engine.RemoveService(ctx, ch.Service); err != nil {
			logger.Warn("remove orphan failed", "container", ch.Service, "err", err)
		}
	}

	// Build the action map for the DAG.
	actions := make(map[string]dag.Action, len(cfg.Services))
	for _, ch := range p.Changes {
		switch ch.Kind {
		case plan.KindCreate:
			actions[ch.Service] = dag.ActionCreate
		case plan.KindRecreate:
			actions[ch.Service] = dag.ActionRecreate
		case plan.KindNoop:
			actions[ch.Service] = dag.ActionNoop
		}
	}

	g, err := dag.Build(cfg.Services)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	var pullMu sync.Mutex
	return g.Run(ctx, engine, stackName, timeout, actions, printProgress, newMakePullSink(isTTY, &pullMu))
}

// --- down ---

func runDown(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()
	return dag.Down(ctx, engine, stackName, downProgress(stackName))
}

// --- teardown ---

func runTeardown(ctx context.Context, configFile string, args []string) error {
	tfs := flag.NewFlagSet("teardown", flag.ExitOnError)
	force := tfs.Bool("force", false, "force-remove images even if other containers reference them")
	if err := tfs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	// 1. Stop + remove all containers and the stack network.
	if err := dag.Down(ctx, engine, stackName, downProgress(stackName)); err != nil {
		return err
	}

	// 2. Remove named volumes declared in the config.
	seen := make(map[string]bool)
	for svcName, svc := range cfg.Services {
		vols, err := config.NormalizeVolumes(svc.Volumes, svc.BaseDir)
		if err != nil {
			logger.Warn("could not parse volumes", "service", svcName, "err", err)
			continue
		}
		for _, v := range vols {
			if v.Type != "volume" || v.Source == "" || seen[v.Source] {
				continue // skip bind mounts and anonymous volumes
			}
			seen[v.Source] = true
			printProgress(v.Source, "removing volume")
			if err := engine.RemoveVolume(ctx, v.Source, *force); err != nil {
				logger.Warn("remove volume failed", "name", v.Source, "err", err)
			}
		}
	}

	// 3. Remove images used by the config.
	seenImg := make(map[string]bool)
	for _, svc := range cfg.Services {
		if svc.Image == "" || seenImg[svc.Image] {
			continue
		}
		seenImg[svc.Image] = true
		printProgress(svc.Image, "removing image")
		if err := engine.RemoveImage(ctx, svc.Image, *force); err != nil {
			logger.Warn("remove image failed", "image", svc.Image, "err", err)
		}
	}

	return nil
}

// --- stop ---

func runStop(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	if len(names) == 0 {
		logger.Debug("no containers found", "stack", stackName)
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

// runStart starts all stopped containers in dependency order, waiting for each
// to become healthy before unblocking its dependents.
func runStart(ctx context.Context, configFile string, timeout time.Duration) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	g, err := dag.Build(cfg.Services)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}

	actions := make(map[string]dag.Action, len(cfg.Services))
	for name := range cfg.Services {
		actions[name] = dag.ActionStart
	}
	// start never pulls images, so use a no-op sink factory.
	return g.Run(ctx, engine, stackName, timeout, actions, printProgress, dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}

// --- restart ---

// runRestart stops all containers concurrently (order doesn't matter when
// stopping everything), then brings them back up in dependency order.
func runRestart(ctx context.Context, configFile string, timeout time.Duration) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	names, err := engine.ListStack(ctx, stackName)
	if err != nil {
		return fmt.Errorf("listing stack: %w", err)
	}
	if len(names) == 0 {
		logger.Debug("no containers found", "stack", stackName)
		return nil
	}

	// Stop all containers concurrently — we're restarting all of them so
	// dependency order doesn't matter here.
	var wg sync.WaitGroup
	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			display := strings.TrimPrefix(name, stackName+"_")
			printProgress(display, "stopping")
			if err := engine.StopService(ctx, name); err != nil {
				logger.Warn("stop failed", "name", name, "err", err)
			}
		}()
	}
	wg.Wait()

	// Start in dependency order, waiting for each service to be healthy.
	g, err := dag.Build(cfg.Services)
	if err != nil {
		return fmt.Errorf("building dependency graph: %w", err)
	}
	actions := make(map[string]dag.Action, len(cfg.Services))
	for name := range cfg.Services {
		actions[name] = dag.ActionStart
	}
	// restart never pulls images, so use a no-op sink factory.
	return g.Run(ctx, engine, stackName, timeout, actions, printProgress, dag.MakePullSink(func(service string) dag.PullSink { return &plainPullSink{service: service} }))
}

// --- ps ---

func runPs(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
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

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return err
	}
	defer engine.Close()

	// If a service name is provided, show only that container.
	var names []string
	if lfs.NArg() > 0 {
		svc := lfs.Arg(0)
		names = []string{svc}
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
		logger.Debug("no containers found", "stack", stackName)
		return nil
	}

	// Single container: stream directly with no prefix.
	if len(names) == 1 {
		return engine.Logs(ctx, names[0], *follow, *tail, os.Stdout)
	}

	// Multiple containers: stream concurrently, prefixing each line with the
	// container name. A shared mutex ensures lines are written atomically.
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, name := range names {
		name := name
		prefix := logPrefix(name, i, isTTY)
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := &prefixWriter{prefix: prefix, out: os.Stdout, mu: &mu}
			if err := engine.Logs(ctx, name, *follow, *tail, w); err != nil {
				logger.Warn("logs failed", "name", name, "err", err)
			}
		}()
	}
	wg.Wait()
	return nil
}

// --- pull sinks ---

// ttyPullSink renders in-place progress for a single image pull when stdout is a TTY.
// All writes share mu so concurrent pulls don't interleave mid-line.
type ttyPullSink struct {
	service string
	mu      *sync.Mutex
}

func (s *ttyPullSink) Begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Print the initial line WITH \n so Write() can cursor-up to overwrite it.
	fmt.Printf("  %-14s %s\n", "pulling", s.service)
}

func (s *ttyPullSink) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n\r ")
	if line == "" {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// \033[A = cursor up one line; \r = start of line; \033[K = erase to end;
	// then write updated content and newline so cursor returns to scratch line.
	fmt.Printf("\033[A\r\033[K  %-14s %s  %s\n", "pulling", s.service, line)
	return len(p), nil
}

func (s *ttyPullSink) Done() {} // last Write() (or Begin() if no events) already placed \n

// plainPullSink is used when stdout is not a TTY; it prints "pulling" once and
// discards streaming progress (since \r updates are meaningless without a terminal).
type plainPullSink struct {
	service string
	printed bool
}

func (s *plainPullSink) Begin() {
	printProgress(s.service, "pulling")
	s.printed = true
}

func (s *plainPullSink) Write(p []byte) (int, error) { return len(p), nil }
func (s *plainPullSink) Done()                        {}

// newMakePullSink returns a MakePullSink factory that produces the right sink
// type based on whether stdout is a TTY. The shared mutex ensures concurrent
// pulls don't interleave their in-place updates.
func newMakePullSink(isTTY bool, mu *sync.Mutex) dag.MakePullSink {
	return func(service string) dag.PullSink {
		if isTTY {
			return &ttyPullSink{service: service, mu: mu}
		}
		return &plainPullSink{service: service}
	}
}

// logPrefix returns a label for a container, with ANSI colour when writing to a TTY.
var logColors = []string{
	"\033[36m", // cyan
	"\033[32m", // green
	"\033[33m", // yellow
	"\033[35m", // magenta
	"\033[34m", // blue
	"\033[31m", // red
}

const logColorReset = "\033[0m"

func logPrefix(name string, idx int, isTTY bool) string {
	if !isTTY {
		return name + " |"
	}
	return logColors[idx%len(logColors)] + name + logColorReset + " |"
}

// prefixWriter buffers output line by line, prepending prefix to each line.
// The shared mu prevents lines from different goroutines interleaving on stdout.
type prefixWriter struct {
	prefix string
	out    *os.File
	mu     *sync.Mutex
	buf    bytes.Buffer
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			pw.buf.Write(p)
			break
		}
		pw.buf.Write(p[:idx+1])
		p = p[idx+1:]
		pw.mu.Lock()
		fmt.Fprintf(pw.out, "%s %s", pw.prefix, pw.buf.String())
		pw.mu.Unlock()
		pw.buf.Reset()
	}
	return n, nil
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

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	var pullMu sync.Mutex
	mkSink := newMakePullSink(isTTY, &pullMu)
	for name, svc := range cfg.Services {
		sink := mkSink(name)
		sink.Begin()
		err := engine.PullImage(ctx, svc.Image, sink)
		sink.Done()
		if err != nil {
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

	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	stackName := resolveStackName(cfg, configFile)
	engine, err := newEngine(ctx, cfg.Engine)
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
	flags := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		flags = append(flags, "-t")
	}
	execArgs := append(append(flags, containerName), cmdArgs...)
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

// --- plan ---

func runPlan(ctx context.Context, configFile string) error {
	cfg, err := config.Load(ctx, configFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	engine, err := newEngine(ctx, cfg.Engine)
	if err != nil {
		return fmt.Errorf("initialising engine: %w", err)
	}
	defer engine.Close()

	stackName := resolveStackName(cfg, configFile)
	result, err := plan.Compute(ctx, cfg, engine, stackName)
	if err != nil {
		return fmt.Errorf("computing plan: %w", err)
	}

	plan.Render(result, os.Stdout)

	for _, ch := range result.Changes {
		if ch.Kind != plan.KindNoop {
			return errPlanHasChanges
		}
	}
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

// printProgress writes a clean, fixed-width progress line to stdout.
// Used by action commands to report what they are doing.
func printProgress(service, status string) {
	fmt.Printf("  %-14s %s\n", status, service)
}

// downProgress returns a Progress func for down/teardown that strips the stack
// name prefix from container names before printing (e.g. "myapp_web" → "web").
func downProgress(stackName string) dag.Progress {
	return func(name, status string) {
		display := strings.TrimPrefix(name, stackName+"_")
		printProgress(display, status)
	}
}

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


func newPodmanEngine(ctx context.Context) (orchestrator.Engine, error) {
	socket := os.Getenv("PODMAN_SOCKET")
	if socket == "" {
		socket = podmanSocket()
	}
	logger.Debug("connecting to podman", "socket", socket)
	return orchestrator.NewPodmanEngine(ctx, socket)
}

// podmanSocket is defined per-platform in socket_darwin.go / socket_linux.go / socket_windows.go.

// resolveStackName returns cfg.Name when set, otherwise derives a deterministic
// adjective-animal name from the absolute path of the config file. The derivation
// is stable so that `dill up` and `dill down` always agree on the stack name for
// the same file, even when no explicit name is configured.
func resolveStackName(cfg *config.DillConfig, configFile string) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	return derivedStackName(configFile)
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

func engineBinary(e orchestrator.Engine) string {
	switch e.(type) {
	case *orchestrator.DockerEngine:
		return "docker"
	default:
		return "podman"
	}
}

