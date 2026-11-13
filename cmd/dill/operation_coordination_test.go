package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

const syntheticDaemon = "synthetic-docker-host-1234"

type coordinatedDockerFixture struct {
	mu                                                   sync.Mutex
	server                                               *http.Server
	socket, stateDir                                     string
	exists, running, orphan, implicitVolumes, dropWrites bool
	labels                                               map[string]string
	image                                                string
	writes, connections                                  int
	uncoordinatedWrite, failTerminal                     bool
	redirectList, blockReads                             bool
	blockWrite                                           bool
	unknownNetworks, extraNetwork                        bool
	networkExists, foreignNetwork, foreignVolume         bool
	writeStarted                                         chan struct{}
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
	f := &coordinatedDockerFixture{socket: filepath.Join(dir, "docker.sock"), stateDir: state, exists: exists, networkExists: exists, image: "synthetic:v1", labels: map[string]string{"dill.managed": "true", "dill.stack": "app", "dill.service": "web"}}
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
		if f.blockWrite {
			// Model an engine effect before the client receives any outcome.
			f.running = false
			select {
			case f.writeStarted <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
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
		settings := map[string]any{}
		if !f.unknownNetworks {
			networks := map[string]any{"app": map[string]any{"NetworkID": strings.Repeat("e", 64)}}
			if f.extraNetwork {
				networks["outside-inventory"] = map[string]any{}
			}
			settings["Networks"] = networks
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("a", 64), "Image": "sha256:" + strings.Repeat("c", 64), "Config": map[string]any{"Image": f.image, "Labels": f.labels}, "HostConfig": map[string]any{"NetworkMode": "bridge"}, "NetworkSettings": settings, "State": map[string]any{"Running": f.running}, "Mounts": []any{}})
	case path == "/networks":
		list := []map[string]string{}
		if f.networkExists {
			list = append(list, map[string]string{"Name": "app", "Id": strings.Repeat("e", 64)})
		}
		json.NewEncoder(w).Encode(list)
	case path == "/networks/app":
		if !f.networkExists {
			w.WriteHeader(404)
			io.WriteString(w, `{"message":"not found"}`)
			return
		}
		owner := "app"
		if f.foreignNetwork {
			owner = "foreign-stack"
		}
		json.NewEncoder(w).Encode(map[string]any{"Name": "app", "Id": strings.Repeat("e", 64), "Driver": "bridge", "Options": map[string]string{}, "Labels": map[string]string{"dill.managed": "true", "dill.stack": owner}})
	case path == "/networks/create":
		f.networkExists = true
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"Id": strings.Repeat("e", 64), "Warning": ""})
	case path == "/volumes/app_data":
		owner := "app"
		if f.foreignVolume {
			owner = "foreign-stack"
		}
		json.NewEncoder(w).Encode(map[string]any{"Name": "app_data", "Driver": "local", "Options": map[string]string{}, "Labels": map[string]string{"dill.managed": "true", "dill.stack": owner}})
	case path == "/containers/create":
		var request struct {
			Labels map[string]string
			Image  string
		}
		json.NewDecoder(r.Body).Decode(&request)
		f.labels = request.Labels
		f.image = request.Image
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
			wantCode, wantOutcome := 0, "succeeded"
			if name == "down" {
				wantCode, wantOutcome = 1, "failed"
			} // existing network retention error contract
			if code != wantCode || result.Outcome != wantOutcome || stderr != "" {
				t.Fatalf("operation: %+v code=%d stderr=%s", result, code, stderr)
			}
			identity, _ := coordination.DockerIdentity(syntheticDaemon)
			if result.Target == nil || result.Target.QualifiedIdentityDigest != "sha256:"+identity {
				t.Fatalf("missing qualified identity: %+v", result.Target)
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
func TestRepeatedQualifiedUpDoesNotRecreatePinnedImage(t *testing.T) {
	fixture := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	for attempt := 0; attempt < 2; attempt++ {
		result, code, _ := captureOperation(t, "", fixture.args("up"))
		if code != 0 || result.Outcome != "succeeded" {
			t.Fatalf("qualified up %d failed: %+v code=%d", attempt, result, code)
		}
		if attempt == 1 {
			for _, resource := range result.Resources {
				if resource.Action == "remove" || resource.Action == "create" || resource.Action == "recreate" || resource.Action == "stop" {
					t.Fatalf("unchanged pinned image forced recreation: %+v", result)
				}
			}
		}
	}
	fixture.mu.Lock()
	image := fixture.image
	fixture.mu.Unlock()
	if image != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("creation did not use immutable prepared image: %s", image)
	}
}

func TestQualifiedSharedResultFixture(t *testing.T) {
	fixture := newCoordinatedDocker(t, true)
	useSyntheticConfig(t, syntheticConfig())
	data, err := os.ReadFile("../../internal/operation/testdata/qualified-success.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		ExpectedExit int              `json:"expectedExit"`
		Expected     operation.Result `json:"expected"`
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	result, code, _ := captureOperation(t, "", fixture.args("stop"))
	result.OperationID = "op_" + strings.Repeat("0", 32)
	// Real temporary endpoint spelling is intentionally unrelated to attested
	// identity; only selector spelling is normalized for shared fixture compare.
	result.Target.EndpointDigest = operation.Digest("unix:///synthetic/qualified.sock")
	if code != expected.ExpectedExit || !reflect.DeepEqual(result, expected.Expected) {
		t.Fatalf("qualified shared fixture mismatch: got=%+v expected=%+v", result, expected.Expected)
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
	for _, scenario := range []string{"pull", "bind", "external", "anonymous", "network", "devices", "capabilities", "tmpfs"} {
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
			case "devices":
				cfg.Services["web"].Devices = []string{"/synthetic/device"}
			case "capabilities":
				cfg.Services["web"].CapAdd = []string{"SYS_ADMIN"}
			case "tmpfs":
				cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`{"type":"tmpfs","target":"/data"}`)}
			}
			useSyntheticConfig(t, cfg)
			result, code, _ := captureOperation(t, "", fixture.args("up"))
			fixture.mu.Lock()
			connections, writes := fixture.connections, fixture.writes
			fixture.mu.Unlock()
			if code != 2 || result.Effects != "none" || connections != 0 || writes != 0 || (result.Target != nil && result.Target.QualifiedIdentityDigest != "") {
				t.Fatalf("unsupported config accessed engine: %+v code=%d connections=%d writes=%d", result, code, connections, writes)
			}
		})
	}
}
func TestQualifiedInventoryBudgetRejectsBeforeSocketOrState(t *testing.T) {
	fixture := newCoordinatedDocker(t, false)
	cfg := syntheticConfig()
	cfg.Services = map[string]*config.Service{}
	for i := 0; i < coordination.MaxResources; i++ {
		cfg.Services[fmt.Sprintf("service%03d", i)] = &config.Service{Image: "synthetic:v1", PullPolicy: "never"}
	}
	useSyntheticConfig(t, cfg)
	result, code, _ := captureOperation(t, "", fixture.args("up"))
	fixture.mu.Lock()
	connections, writes := fixture.connections, fixture.writes
	fixture.mu.Unlock()
	entries, err := os.ReadDir(fixture.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if code != 2 || result.Outcome != "invalid_input" || result.Effects != "none" || connections != 0 || writes != 0 || len(entries) != 0 {
		t.Fatalf("oversized inventory accessed target/state: %+v code=%d connections=%d writes=%d entries=%d", result, code, connections, writes, len(entries))
	}
}

func TestQualifiedForeignResourcesFailBeforeAnyHTTPWrite(t *testing.T) {
	for _, kind := range []string{"network", "volume"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, false)
			cfg := syntheticConfig()
			fixture.mu.Lock()
			if kind == "network" {
				fixture.networkExists = true
				fixture.foreignNetwork = true
			} else {
				fixture.foreignVolume = true
				cfg.Services["web"].Volumes = []json.RawMessage{json.RawMessage(`"data:/data"`)}
			}
			fixture.mu.Unlock()
			useSyntheticConfig(t, cfg)
			result, code, _ := captureOperation(t, "", fixture.args("up"))
			fixture.mu.Lock()
			writes := fixture.writes
			fixture.mu.Unlock()
			if code != 1 || result.Effects != "none" || writes != 0 {
				t.Fatalf("foreign %s caused earlier mutation: %+v code=%d HTTP writes=%d", kind, result, code, writes)
			}
		})
	}
}

func TestQualifiedRequiresActualAttachedNetworkEvidence(t *testing.T) {
	for _, mode := range []string{"unknown", "extra"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, true)
			fixture.mu.Lock()
			fixture.unknownNetworks = mode == "unknown"
			fixture.extraNetwork = mode == "extra"
			fixture.mu.Unlock()
			useSyntheticConfig(t, syntheticConfig())
			result, code, _ := captureOperation(t, "", fixture.args("start"))
			fixture.mu.Lock()
			writes := fixture.writes
			fixture.mu.Unlock()
			if code != 2 || result.Effects != "none" || writes != 0 {
				t.Fatalf("unqualified actual attachments allowed: %+v code=%d HTTP writes=%d", result, code, writes)
			}
		})
	}
}

func TestQualifiedAutomaticGuardrailsComposeWithExplicitProfile(t *testing.T) {
	fixture := newCoordinatedDocker(t, false)
	useSyntheticConfig(t, syntheticConfig())
	result, code, _ := captureOperation(t, "", fixture.args("up", "--alpha-disposable"))
	if code != 0 || result.Outcome != "succeeded" {
		t.Fatalf("canonical capability forwarding lost across wrappers: %+v code=%d", result, code)
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
func TestQualifiedCloseFailuresKeepReceiptAndJournalCoherent(t *testing.T) {
	for _, phase := range []string{"transport", "post-commit-lock"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newCoordinatedDocker(t, true)
			useSyntheticConfig(t, syntheticConfig())
			oldTarget, oldSession := closeCoordinatedTarget, closeCoordinatedSession
			defer func() { closeCoordinatedTarget = oldTarget; closeCoordinatedSession = oldSession }()
			if phase == "transport" {
				closeCoordinatedTarget = func(target *coordination.Target) error {
					_ = target.Close()
					return errors.New("PRIVATE_CLOSE_DIAGNOSTIC")
				}
			} else {
				closeCoordinatedSession = func(session *coordination.Session) error {
					_ = session.Close()
					return errors.New("PRIVATE_RELEASE_DIAGNOSTIC")
				}
			}
			result, code, _ := captureOperation(t, "", fixture.args("stop"))
			closeCoordinatedTarget, closeCoordinatedSession = oldTarget, oldSession
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			state, err := coordination.Open(ctx, fixture.stateDir)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			target, _ := coordination.DockerIdentity(syntheticDaemon)
			record, err := state.Inspect(ctx, target, "app")
			if err != nil || record == nil || code != 1 || record.OperationID != result.OperationID || record.Outcome != result.Outcome || record.Effects != result.Effects {
				t.Fatalf("close failure diverged: receipt=%+v journal=%+v code=%d err=%v", result, record, code, err)
			}
			for _, failure := range result.Errors {
				if strings.Contains(failure.Message, "PRIVATE_") {
					t.Fatal("close diagnostic leaked")
				}
			}
			if phase == "transport" {
				if result.Outcome != "unknown" || result.Effects != "unknown" {
					t.Fatal("transport uncertainty was not persisted", result)
				}
				fixture.mu.Lock()
				writes := fixture.writes
				fixture.mu.Unlock()
				retry, code, _ := captureOperation(t, "", fixture.args("stop"))
				fixture.mu.Lock()
				later := fixture.writes
				fixture.mu.Unlock()
				if code != 1 || retry.Effects != "none" || later != writes {
					t.Fatal("unknown close outcome was replayed", retry)
				}
			} else if result.Outcome != "succeeded" || result.Effects != "complete" {
				t.Fatal("post-commit cleanup erased known effects", result)
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
		// Inject a failed-return boundary with a genuinely partial on-disk
		// reservation inventory. Real persistence fault windows/StartError
		// production are separately exercised inside coordination tests.
		key, err := os.ReadFile(filepath.Join(fixture.stateDir, "identity.key"))
		if err != nil {
			return nil, err
		}
		token := func(domain, value string) string {
			h := hmac.New(sha256.New, key)
			h.Write([]byte(domain))
			h.Write([]byte{0})
			h.Write([]byte(value))
			return hex.EncodeToString(h.Sum(nil))
		}
		resource := resources[0]
		resourceID := token("resource:"+resource.Kind, resource.Name)
		lockID := token("resource-lock:"+target.Identity()+":"+resource.Kind, resourceID)
		if err = os.Remove(filepath.Join(fixture.stateDir, lockID+".json")); err != nil {
			return nil, err
		}
		directory, err := os.Open(fixture.stateDir)
		if err != nil {
			return nil, err
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return nil, err
		}
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
	if second.OperationID == first.OperationID || len(second.Resources) != 0 {
		t.Fatal("prior unresolved operation ID/resources leaked into new receipt")
	}
	for _, failure := range second.Errors {
		if strings.Contains(failure.Message, first.OperationID) {
			t.Fatal("old operation ID leaked through fixed diagnostic")
		}
	}
	if code != 1 || second.Effects != "none" || laterWrites != writes || connections != 2 {
		t.Fatalf("blind retry or hidden SDK reconnect: first=%+v second=%+v writes=%d/%d connections=%d", first, second, writes, laterWrites, connections)
	}
}
