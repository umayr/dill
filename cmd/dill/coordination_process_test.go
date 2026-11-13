package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

func TestCoordinationMainHelper(t *testing.T) {
	mode := os.Getenv("DILL_COORD_MAIN_TEST")
	if mode == "" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"dill"}, os.Args[i+1:]...)
			break
		}
	}
	if mode == "operation" {
		qualifiedNativeHost = func() bool { return true }
	}
	if mode == "unsupported-host" {
		qualifiedNativeHost = func() bool { return false }
	}
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		if mode != "operation" {
			panic("offline coordination evaluated configuration")
		}
		return syntheticConfig(), nil
	}
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		panic("coordination opened ambient engine")
	}
	main()
}
func coordinationProcess(mode string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestCoordinationMainHelper$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "DILL_COORD_MAIN_TEST="+mode, "DILL_PKL_PATH=/synthetic/missing-pkl", "DOCKER_HOST=tcp://synthetic.invalid:1")
	return cmd
}
func runCoordinationProcess(t *testing.T, mode string, args ...string) (string, string, int) {
	t.Helper()
	cmd := coordinationProcess(mode, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return out.String(), stderr.String(), code
}
func TestQualifiedLinuxHostDiscovery(t *testing.T) {
	out, stderr, code := runCoordinationProcess(t, "operation", "operation-capabilities")
	var discovery struct{ Features, QualifiedCoordinationOperations, QualifiedCoordinationEngines, QualifiedCoordinationLayouts []string }
	if json.Unmarshal([]byte(out), &discovery) != nil || code != 0 || stderr != "" || strings.Join(discovery.QualifiedCoordinationOperations, ",") != strings.Join(qualifiedOperations, ",") || strings.Join(discovery.QualifiedCoordinationEngines, ",") != "docker" || strings.Join(discovery.QualifiedCoordinationLayouts, ",") != coordination.NativeDockerLayout {
		t.Fatalf("supported Linux host qualification not advertised truthfully: %s %s %d", out, stderr, code)
	}
	found := false
	for _, feature := range discovery.Features {
		found = found || feature == "optInQualifiedLocalCoordination"
	}
	if !found {
		t.Fatal("supported Linux host qualification feature missing")
	}
}

func TestQualifiedUnsupportedHostRejectsBeforeSocketStateAndDiscovery(t *testing.T) {
	fixture := newCoordinatedDocker(t, false)
	out, stderr, code := runCoordinationProcess(t, "unsupported-host", append([]string{"operation"}, fixture.args("up")...)...)
	var result operation.Result
	if json.Unmarshal([]byte(out), &result) != nil || code != 2 || stderr != "" || result.Outcome != "invalid_input" || result.Effects != "none" || result.Target != nil {
		t.Fatalf("unsupported host was not rejected before access: %s %s %d", out, stderr, code)
	}
	out, stderr, code = runCoordinationProcess(t, "unsupported-host", "operation-capabilities")
	var discovery struct{ Operations, Features, QualifiedCoordinationOperations, QualifiedCoordinationEngines, QualifiedCoordinationLayouts []string }
	if json.Unmarshal([]byte(out), &discovery) != nil || code != 0 || stderr != "" || len(discovery.Operations) == 0 || len(discovery.QualifiedCoordinationOperations) != 0 || len(discovery.QualifiedCoordinationEngines) != 0 || len(discovery.QualifiedCoordinationLayouts) != 0 {
		t.Fatalf("unsupported host advertised qualification: %s %s %d", out, stderr, code)
	}
	for _, feature := range discovery.Features {
		if feature == "optInQualifiedLocalCoordination" {
			t.Fatal("unsupported host advertised qualification feature")
		}
	}
	fixture.mu.Lock()
	connections, writes := fixture.connections, fixture.writes
	fixture.mu.Unlock()
	entries, err := os.ReadDir(fixture.stateDir)
	if err != nil || connections != 0 || writes != 0 || len(entries) != 0 {
		t.Fatalf("unsupported host accessed target/state: connections=%d writes=%d entries=%d err=%v", connections, writes, len(entries), err)
	}
}

func TestOfflineRecoveryRootRoutingAndExactAcknowledgement(t *testing.T) {
	fixture := newCoordinatedDocker(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target, err := coordination.Qualify(ctx, coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: fixture.socket, DaemonID: syntheticDaemon})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	state, err := coordination.Open(ctx, fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	session, err := state.Begin(ctx, target, "protocol", "brine", []coordination.ResourceInput{{Kind: "container", Name: "private-name", Action: "reconcile"}})
	if err != nil {
		t.Fatal(err)
	}
	id := session.OperationID()
	session.Close()
	args := []string{"coordination", "inspect", "--state-dir", fixture.stateDir, "--target-id", "sha256:" + target.Identity(), "--stack", "protocol"}
	if isProtocolInvocation(args) {
		t.Fatal("offline namespace misrouted to metadata protocol")
	}
	out, stderr, code := runCoordinationProcess(t, "offline", args...)
	if code != 0 || stderr != "" || !strings.Contains(out, id) || strings.Contains(out, "private-name") {
		t.Fatalf("offline inspect: %s %s %d", out, stderr, code)
	}
	ack := append([]string{}, args...)
	ack[1] = "acknowledge"
	ack = append(ack, "--operation-id", id)
	_, stderr, code = runCoordinationProcess(t, "offline", ack...)
	if code != 2 || stderr != "" {
		t.Fatal("acknowledged without explicit inspected intent")
	}
	ack = append(ack, "--inspected")
	out, stderr, code = runCoordinationProcess(t, "offline", ack...)
	if code != 0 || stderr != "" || !strings.Contains(out, `"state":"acknowledged"`) {
		t.Fatalf("offline ack: %s %s %d", out, stderr, code)
	}
	// Offline inspect/ack adds zero daemon connections beyond our setup proof.
	fixture.mu.Lock()
	connections := fixture.connections
	fixture.mu.Unlock()
	if connections != 1 {
		t.Fatal("offline recovery contacted engine")
	}
	// Parser errors cannot route to env-file loading or echo private arguments.
	sentinel := filepath.Join(t.TempDir(), "PRIVATE_SENTINEL")
	out, stderr, code = runCoordinationProcess(t, "offline", "coordination", "inspect", "--env-file", sentinel, "--stack", "protocol")
	if code != 2 || stderr != "" || strings.Contains(out, "PRIVATE_SENTINEL") {
		t.Fatalf("unsafe offline parser: %s %s %d", out, stderr, code)
	}
}
func TestQualifiedCLIAndBrineProcessContention(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target, err := coordination.Qualify(ctx, coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: fixture.socket, DaemonID: syntheticDaemon})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	state, err := coordination.Open(ctx, fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	held, err := state.Begin(ctx, target, "app", "brine", nil)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(fixture.socket), "socket-alias.sock")
	if err := os.Symlink(fixture.socket, alias); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"operation"}, fixture.args("stop", "--timeout", "80ms", "--engine-socket", alias, "-f", "/synthetic/alias-config.pkl")...)
	out, stderr, code := runCoordinationProcess(t, "operation", args...)
	var result operation.Result
	if json.Unmarshal([]byte(out), &result) != nil || code != 1 || stderr != "" || result.Outcome != "cancelled" || result.Effects != "none" {
		t.Fatalf("bounded process contention: %s %s %d", out, stderr, code)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if writes != 0 {
		t.Fatal("waiter mutated under active lock")
	}
	if err = held.Finish("succeeded", "none"); err != nil {
		t.Fatal(err)
	}
	held.Close()
	// Independently launched CLI and Brine processes cooperate on identical keys.
	var workers sync.WaitGroup
	results := make(chan string, 2)
	for _, source := range []string{"cli", "brine"} {
		workers.Add(1)
		go func(source string) {
			defer workers.Done()
			args := append([]string{"operation"}, fixture.args("stop", "--coordination-source", source)...)
			cmd := coordinationProcess("operation", args...)
			data, err := cmd.CombinedOutput()
			if err != nil {
				results <- fmt.Sprintf("%s: %v %s", source, err, data)
				return
			}
			var result operation.Result
			if json.Unmarshal(data, &result) != nil || result.Outcome != "succeeded" {
				results <- fmt.Sprintf("%s invalid result: %s", source, data)
				return
			}
			results <- ""
		}(source)
	}
	workers.Wait()
	close(results)
	for failure := range results {
		if failure != "" {
			t.Fatal(failure)
		}
	}
}
func TestQualifiedProcessDeathAfterDaemonEffectRequiresInspection(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	fixture.mu.Lock()
	fixture.blockWrite = true
	fixture.writeStarted = make(chan struct{}, 1)
	fixture.mu.Unlock()
	args := append([]string{"operation"}, fixture.args("stop", "--coordination-source", "brine")...)
	cmd := coordinationProcess("operation", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	select {
	case <-fixture.writeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("child never reached synthetic daemon effect")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child succeeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := coordination.Open(ctx, fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	target, _ := coordination.DockerIdentity(syntheticDaemon)
	record, err := state.Inspect(ctx, target, "app")
	if err != nil || record == nil || record.State != "in_progress" || record.Outcome != "unknown" || record.Effects != "unknown" {
		t.Fatalf("lost crashed effect: %+v %v", record, err)
	}
	// A fresh CLI process must not turn kernel lock release into blind replay.
	out, stderrText, code := runCoordinationProcess(t, "operation", append([]string{"operation"}, fixture.args("stop")...)...)
	var result operation.Result
	if json.Unmarshal([]byte(out), &result) != nil || code != 1 || stderrText != "" || result.Effects != "none" {
		t.Fatalf("crash retry outcome: %s %s %d", out, stderrText, code)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	running := fixture.running
	fixture.mu.Unlock()
	if writes != 1 || running {
		t.Fatalf("effect missing or blindly replayed: writes=%d running=%v", writes, running)
	}
}

func TestDockerSDKQualifiedRedirectAndDeadlineFailClosed(t *testing.T) {
	for _, mode := range []string{"redirect", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, true)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target, err := coordination.Qualify(ctx, coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: fixture.socket, DaemonID: syntheticDaemon})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			engine, err := orchestrator.NewDockerEngineWithClient(coordination.Endpoint, target.Client())
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			if _, err = engine.ListStack(ctx, "app"); err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			fixture.redirectList = mode == "redirect"
			fixture.blockReads = mode == "deadline"
			fixture.mu.Unlock()
			short, stop := context.WithTimeout(context.Background(), 60*time.Millisecond)
			_, err = engine.ListStack(short, "app")
			stop()
			if err == nil {
				t.Fatal("accepted redirect or timed-out request")
			}
			fixture.mu.Lock()
			fixture.redirectList = false
			fixture.blockReads = false
			connections := fixture.connections
			fixture.mu.Unlock()
			if mode == "deadline" {
				if _, err = engine.ListStack(ctx, "app"); err == nil {
					t.Fatal("SDK reconnected after request cancellation")
				}
			}
			if connections != 1 {
				t.Fatal("SDK silently opened another connection")
			}
		})
	}
}

func TestDockerSDKQualifiedTransportReplacementAndParallelReads(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	first := newCoordinatedDocker(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target, err := coordination.Qualify(ctx, coordination.Qualification{Layout: coordination.NativeDockerLayout, Socket: first.socket, DaemonID: syntheticDaemon})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	engine, err := orchestrator.NewDockerEngineWithClient(coordination.Endpoint, target.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	// Negotiate via SDK before parallel requests; SDK version negotiation itself
	// must also share the /info connection.
	if _, err = engine.ListStack(ctx, "app"); err != nil {
		t.Fatal(err)
	}
	second := newCoordinatedDocker(t, true)
	if err = os.Rename(first.socket, first.socket+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(second.socket, first.socket); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := engine.ListStack(ctx, "app"); failures <- err }()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	second.mu.Lock()
	connections := second.connections
	second.mu.Unlock()
	if connections != 0 {
		t.Fatal("SDK retargeted replacement socket")
	}
	first.server.Close()
	if _, err = engine.ListStack(ctx, "app"); err == nil {
		t.Fatal("SDK reconnected after qualified connection loss")
	}
	second.mu.Lock()
	connections = second.connections
	second.mu.Unlock()
	if connections != 0 {
		t.Fatal("SDK hidden reconnect to replacement daemon")
	}
}
