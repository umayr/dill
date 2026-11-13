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
	args := append([]string{"operation"}, fixture.args("stop", "--timeout", "80ms")...)
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
