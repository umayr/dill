package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/orchestrator"
)

const syntheticDaemon = "synthetic-docker-host-1234"

type coordinatedDockerFixture struct {
	mu                                                   sync.Mutex
	server                                               *http.Server
	socket, stateDir                                     string
	exists, running, orphan, implicitVolumes, dropWrites bool
	labels                                               map[string]string
	writes, connections                                  int
	uncoordinatedWrite, failTerminal                     bool
	redirectList, blockReads                             bool
}

func newCoordinatedDocker(t *testing.T, exists bool) *coordinatedDockerFixture {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dill-sdk-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	if err = os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	f := &coordinatedDockerFixture{socket: filepath.Join(dir, "docker.sock"), stateDir: state, exists: exists, labels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}
	listener, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	f.server = &http.Server{Handler: http.HandlerFunc(f.serve), ConnState: func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			f.mu.Lock()
			f.connections++
			f.mu.Unlock()
		}
	}}
	go f.server.Serve(listener)
	t.Cleanup(func() { f.server.Close() })
	return f
}

var apiPrefix = regexp.MustCompile(`^/v[0-9.]+`)

func (f *coordinatedDockerFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := apiPrefix.ReplaceAllString(r.URL.Path, "")
	w.Header().Set("Content-Type", "application/json")
	if path == "/info" {
		fmt.Fprintf(w, `{"ID":%q}`, syntheticDaemon)
		return
	}
	if path == "/_ping" {
		w.Header().Set("API-Version", "1.44")
		io.WriteString(w, "OK")
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		f.writes++
		heads, _ := filepath.Glob(filepath.Join(f.stateDir, "*.json"))
		if len(heads) < 3 {
			f.uncoordinatedWrite = true
		}
		for _, path := range heads {
			data, err := os.ReadFile(path)
			var record coordination.Record
			if err != nil || json.Unmarshal(data, &record) != nil || record.State != "in_progress" {
				f.uncoordinatedWrite = true
			}
			if f.failTerminal && record.OperationID != "" {
				os.Mkdir(filepath.Join(f.stateDir, record.OperationID+".result"), 0700)
			}
		}
		if f.dropWrites {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
	}
	switch {
	case path == "/containers/json":
		if f.redirectList {
			w.Header().Set("Location", "http://outside.invalid/private")
			w.WriteHeader(302)
			return
		}
		if f.blockReads {
			<-r.Context().Done()
			return
		}
		list := []map[string]any{}
		if f.exists {
			state := "exited"
			if f.running {
				state = "running"
			}
			list = append(list, map[string]any{"Id": strings.Repeat("a", 64), "Names": []string{"/app_web"}, "Labels": f.labels, "State": state, "Image": "synthetic:v1"})
		}
		if f.orphan {
			list = append(list, map[string]any{"Id": strings.Repeat("d", 64), "Names": []string{"/app_orphan"}})
		}
		json.NewEncoder(w).Encode(list)
	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		volumes := map[string]any{}
		if f.implicitVolumes {
			volumes["/implicit-private"] = struct{}{}
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + strings.Repeat("c", 64), "Config": map[string]any{"Volumes": volumes}})
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		if !f.exists {
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"not found"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("a", 64), "Image": "sha256:" + strings.Repeat("c", 64), "Config": map[string]any{"Image": "synthetic:v1", "Labels": f.labels}, "HostConfig": map[string]any{"NetworkMode": "app"}, "NetworkSettings": map[string]any{}, "State": map[string]any{"Running": f.running}, "Mounts": []any{}})
	case path == "/networks":
		json.NewEncoder(w).Encode([]map[string]string{{"Name": "app", "Id": strings.Repeat("e", 64)}})
	case path == "/networks/app":
		w.WriteHeader(404)
		io.WriteString(w, `{"message":"not found"}`)
	case path == "/containers/create":
		var request struct{ Labels map[string]string }
		json.NewDecoder(r.Body).Decode(&request)
		f.labels = request.Labels
		f.exists = true
		f.running = false
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("a", 64), "Warnings": []string{}})
	case strings.HasSuffix(path, "/start"):
		f.running = true
		w.WriteHeader(204)
	case strings.HasSuffix(path, "/stop"):
		f.running = false
		w.WriteHeader(204)
	case strings.HasPrefix(path, "/containers/") && r.Method == "DELETE":
		f.exists = false
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
		fmt.Fprintf(w, `{"message":"unsupported synthetic route %s"}`, path)
	}
}
func (f *coordinatedDockerFixture) args(name string, extra ...string) []string {
	args := []string{name, "--contract", "dill.operation/1", "--engine", "docker", "--engine-socket", f.socket, "-p", "app", "--coordination-state-dir", f.stateDir, "--coordination-layout", coordination.NativeDockerLayout, "--coordination-daemon-id", syntheticDaemon, "--coordination-source", "cli", "--timeout", "3s"}
	return append(args, extra...)
}
func syntheticConfig() *config.DillConfig {
	return &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{"web": {Image: "synthetic:v1", PullPolicy: "never"}}}
}
func useSyntheticConfig(t *testing.T, cfg *config.DillConfig) {
	t.Helper()
	oldLoader, oldFactory := configLoader, engineFactory
	configLoader = func(context.Context, string) (*config.DillConfig, error) { return cfg, nil }
	// Qualified mode MUST NOT create a separately connected ambient factory engine.
	engineFactory = func(context.Context, string, string) (orchestrator.Engine, error) {
		t.Error("qualified operation used unpinned factory")
		return nil, fmt.Errorf("unpinned engine forbidden")
	}
	t.Cleanup(func() { configLoader = oldLoader; engineFactory = oldFactory })
}
func TestQualifiedOperationAllSupportedHandlersUseDockerSDK(t *testing.T) {
	for _, name := range qualifiedOperations {
		t.Run(name, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, name != "up")
			useSyntheticConfig(t, syntheticConfig())
			result, code, stderr := captureOperation(t, "", fixture.args(name, "--yes"))
			if code != 0 || result.Outcome != "succeeded" || stderr != "" {
				t.Fatalf("operation: %+v code=%d stderr=%s", result, code, stderr)
			}
			fixture.mu.Lock()
			connections, writes, uncoordinated := fixture.connections, fixture.writes, fixture.uncoordinatedWrite
			fixture.mu.Unlock()
			if connections != 1 || writes == 0 || uncoordinated {
				t.Fatalf("SDK did not reuse single qualified connection: connections=%d writes=%d", connections, writes)
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
			if err != nil || record == nil || record.OperationID != result.OperationID || record.Outcome != result.Outcome || record.Effects != result.Effects {
				t.Fatalf("journal/result mismatch: %+v %+v %v", record, result, err)
			}
		})
	}
}
func TestQualifiedUnsupportedInvocationsHaveNoEngineAccess(t *testing.T) {
	old := configLoader
	defer func() { configLoader = old }()
	configLoader = func(context.Context, string) (*config.DillConfig, error) {
		t.Error("evaluated unsupported qualified request")
		return nil, fmt.Errorf("not permitted")
	}
	for _, args := range [][]string{
		operationArgs("teardown", "docker", "--coordination-state-dir", "/synthetic/private"),
		operationArgs("up", "podman", "--coordination-state-dir", "/synthetic/private"),
		operationArgs("up", "docker", "--coordination-layout="),
		operationArgs("validate", "docker", "--coordination-source", "cli"),
	} {
		result, code, _ := captureOperation(t, "", args)
		if code != 2 || result.Effects != "none" {
			t.Fatalf("unsafe rejection: %+v code=%d", result, code)
		}
	}
}
func TestQualifiedInvalidConfigNoSocketOrStateAccess(t *testing.T) {
	for _, scenario := range []string{"pull", "bind", "external", "anonymous", "network"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, false)
			cfg := syntheticConfig()
			switch scenario {
			case "pull":
				cfg.Services["web"].PullPolicy = "missing"
			case "bind":
				cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"/synthetic-private:/data"`)}
			case "external":
				cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`{"type":"volume","source":"external-private","target":"/data","external":true}`)}
			case "anonymous":
				cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"/data"`)}
			case "network":
				cfg.Services["web"].NetworkMode = "host"
			}
			useSyntheticConfig(t, cfg)
			result, code, _ := captureOperation(t, "", fixture.args("up"))
			fixture.mu.Lock()
			connections, writes := fixture.connections, fixture.writes
			fixture.mu.Unlock()
			if code != 2 || result.Effects != "none" || connections != 0 || writes != 0 {
				t.Fatalf("unsupported config accessed engine: %+v code=%d connections=%d writes=%d", result, code, connections, writes)
			}
		})
	}
}
func TestQualifiedLiveOrImageBoundaryFailsBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"orphan", "image-volumes"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, false)
			fixture.orphan = scenario == "orphan"
			fixture.implicitVolumes = scenario == "image-volumes"
			useSyntheticConfig(t, syntheticConfig())
			result, code, _ := captureOperation(t, "", fixture.args("up"))
			fixture.mu.Lock()
			writes := fixture.writes
			fixture.mu.Unlock()
			if code == 0 || result.Effects != "none" || writes != 0 {
				t.Fatalf("unsupported live layout mutated: %+v writes=%d", result, writes)
			}
		})
	}
}
func TestQualifiedFailedStartKeepsDurableOperationID(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	old := beginCoordinatedOperation
	beginCoordinatedOperation = func(ctx context.Context, state *coordination.State, target *coordination.Target, stack, source string, resources []coordination.ResourceInput) (*coordination.Session, error) {
		session, err := state.Begin(ctx, target, stack, source, resources)
		if err != nil {
			return nil, err
		}
		id := session.OperationID()
		session.Close()
		return nil, &coordination.StartError{OperationID: id}
	}
	defer func() { beginCoordinatedOperation = old }()
	result, code, _ := captureOperation(t, "", fixture.args("stop"))
	if code != 1 || result.Effects != "none" {
		t.Fatalf("failed start mutated: %+v code=%d", result, code)
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
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	if err != nil || record == nil || record.OperationID != result.OperationID || record.State != "in_progress" || writes != 0 {
		t.Fatalf("failed start ID correlation lost: record=%+v result=%+v writes=%d err=%v", record, result, writes, err)
	}
}

func TestQualifiedTerminalFailureBecomesUnknownAndBlocksRetry(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	fixture.failTerminal = true
	useSyntheticConfig(t, syntheticConfig())
	result, code, _ := captureOperation(t, "", fixture.args("stop"))
	if code != 1 || result.Outcome != "unknown" || result.Effects != "unknown" {
		t.Fatalf("terminal persistence failure claimed success: %+v %d", result, code)
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
	if err != nil || record == nil || record.State != "in_progress" || record.OperationID != result.OperationID {
		t.Fatalf("terminal failure lost unknown: %+v %v", record, err)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	retry, code, _ := captureOperation(t, "", fixture.args("stop"))
	fixture.mu.Lock()
	later := fixture.writes
	fixture.mu.Unlock()
	if code != 1 || retry.Effects != "none" || later != writes {
		t.Fatalf("blind replay after terminal failure: %+v writes=%d/%d", retry, writes, later)
	}
}

func TestQualifiedDisconnectNoRetryAndBlocksNextOperation(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	fixture.dropWrites = true
	useSyntheticConfig(t, syntheticConfig())
	first, code, _ := captureOperation(t, "", fixture.args("stop"))
	if code != 1 || first.Outcome != "unknown" || first.Effects != "unknown" {
		t.Fatalf("lost uncertainty: %+v %d", first, code)
	}
	fixture.mu.Lock()
	writes := fixture.writes
	fixture.mu.Unlock()
	second, code, _ := captureOperation(t, "", fixture.args("stop"))
	fixture.mu.Lock()
	laterWrites, connections := fixture.writes, fixture.connections
	fixture.mu.Unlock()
	if code != 1 || second.Effects != "none" || laterWrites != writes || connections != 2 {
		t.Fatalf("blind retry or hidden SDK reconnect: first=%+v second=%+v writes=%d/%d connections=%d", first, second, writes, laterWrites, connections)
	}
}
