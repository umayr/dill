//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This consumer deliberately does not import internal/operation: the executable
// and the complete wire envelope, not an in-process recorder mock, are tested.
// API origin: e1a5e2a99341aa7e235c2b3ab98ae2ad45b1c1cc docs/operation-v1.md.
// Negotiated additive target field: approved #78 86e9d07a2bebe515aeeec48729e29c5ecb0a4d8f.
type alphaOperationResult struct {
	Version     string `json:"version"`
	OperationID string `json:"operationId"`
	Operation   string `json:"operation"`
	Target      *struct {
		Engine                  string `json:"engine"`
		EndpointDigest          string `json:"endpointDigest"`
		QualifiedIdentityDigest string `json:"qualifiedIdentityDigest,omitempty"`
	} `json:"target"`
	Stack           string `json:"stack"`
	Outcome         string `json:"outcome"`
	Effects         string `json:"effects"`
	ChangesDetected bool   `json:"changesDetected"`
	Resources       []struct {
		Kind           string `json:"kind"`
		IdentityDigest string `json:"identityDigest"`
		Action         string `json:"action"`
		Effect         string `json:"effect"`
	} `json:"resources"`
	ResourcesTruncated bool `json:"resourcesTruncated"`
	Errors             []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func alphaDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodeAlphaOperation(data []byte) (alphaOperationResult, error) {
	var result alphaOperationResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return result, fmt.Errorf("expected exactly one terminal document")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return result, err
	}
	if err := alphaRequiredFields(fields, "version", "operationId", "operation", "stack", "outcome", "effects", "changesDetected", "resources", "resourcesTruncated", "errors"); err != nil {
		return result, err
	}
	target, present := fields["target"]
	if !present {
		return result, fmt.Errorf("missing target field")
	}
	if bytes.Equal(bytes.TrimSpace(target), []byte("null")) {
		if (result.Outcome != "invalid_input" && result.Outcome != "failed") || result.Stack != "" || result.Effects != "none" || len(result.Resources) != 0 {
			return result, fmt.Errorf("null target outside an early no-effect failure")
		}
	} else {
		var targetFields map[string]json.RawMessage
		if err := json.Unmarshal(target, &targetFields); err != nil {
			return result, err
		}
		if err := alphaRequiredFields(targetFields, "engine", "endpointDigest"); err != nil {
			return result, err
		}
		if _, present := targetFields["qualifiedIdentityDigest"]; present {
			if err := alphaRequiredFields(targetFields, "qualifiedIdentityDigest"); err != nil {
				return result, err
			}
			if result.Target.Engine != "docker" || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(result.Target.QualifiedIdentityDigest) {
				return result, fmt.Errorf("invalid qualified target digest")
			}
		}
	}
	for name, required := range map[string][]string{
		"resources": {"kind", "identityDigest", "action", "effect"},
		"errors":    {"code", "message"},
	} {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &entries); err != nil {
			return result, err
		}
		for _, entry := range entries {
			if err := alphaRequiredFields(entry, required...); err != nil {
				return result, err
			}
		}
	}
	if result.Version != "dill.operation/1" || !regexp.MustCompile(`^op_[0-9a-f]{32}$`).MatchString(result.OperationID) {
		return result, fmt.Errorf("invalid contract or operation ID")
	}
	if result.Resources == nil || result.Errors == nil || result.ResourcesTruncated {
		return result, fmt.Errorf("null or truncated bounded baseline observations")
	}
	return result, nil
}

func alphaRequiredFields(fields map[string]json.RawMessage, names ...string) error {
	for _, name := range names {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("missing or null required field %s", name)
		}
	}
	return nil
}

func TestAlphaOperationEnvelopeDecoder(t *testing.T) {
	valid := `{"version":"dill.operation/1","operationId":"op_0123456789abcdef0123456789abcdef","operation":"validate","target":null,"stack":"","outcome":"invalid_input","effects":"none","changesDetected":false,"resources":[],"resourcesTruncated":false,"errors":[]}`
	if _, err := decodeAlphaOperation([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{valid + valid, "progress\n" + valid, strings.Replace(valid, `"errors":[]`, `"errors":null`, 1), strings.Replace(valid, `"changesDetected":false,`, "", 1), strings.Replace(valid, `"effects":"none"`, `"effects":"none","unexpected":true`, 1)} {
		if _, err := decodeAlphaOperation([]byte(bad)); err == nil {
			t.Fatal("accepted malformed or incomplete terminal result")
		}
	}
}

func TestAlphaOperationEnvelopeDecoderNullFields(t *testing.T) {
	base := map[string]any{
		"version": "dill.operation/1", "operationId": "op_0123456789abcdef0123456789abcdef", "operation": "validate",
		"target": map[string]any{"engine": "docker", "endpointDigest": alphaDigest("unix:///synthetic.sock")},
		"stack":  "synthetic", "outcome": "succeeded", "effects": "none", "changesDetected": false,
		"resources": []any{}, "resourcesTruncated": false, "errors": []any{},
	}
	assertRejected := func(t *testing.T, value map[string]any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeAlphaOperation(data); err == nil {
			t.Fatal("accepted missing/null nonnullable contract field")
		}
	}
	for field := range base {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("root/%s/missing-%v", field, missing), func(t *testing.T) {
				value := make(map[string]any, len(base))
				for key, item := range base {
					value[key] = item
				}
				if missing {
					delete(value, field)
				} else {
					value[field] = nil
				}
				assertRejected(t, value)
			})
		}
	}
	for container, template := range map[string]map[string]any{
		"target":    {"engine": "docker", "endpointDigest": alphaDigest("unix:///synthetic.sock")},
		"resources": {"kind": "service", "identityDigest": alphaDigest("synthetic/app"), "action": "noop", "effect": "none"},
		"errors":    {"code": "INVALID_INPUT", "message": "Invalid operation input."},
	} {
		for field := range template {
			for _, missing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/missing-%v", container, field, missing), func(t *testing.T) {
					value := make(map[string]any, len(base))
					for key, item := range base {
						value[key] = item
					}
					entry := make(map[string]any, len(template))
					for key, item := range template {
						entry[key] = item
					}
					if missing {
						delete(entry, field)
					} else {
						entry[field] = nil
					}
					value[container] = entry
					if container != "target" {
						value[container] = []any{entry}
					}
					assertRejected(t, value)
				})
			}
		}
	}
	for _, field := range []string{"resources", "errors"} {
		value := make(map[string]any, len(base))
		for key, item := range base {
			value[key] = item
		}
		value[field] = []any{nil}
		assertRejected(t, value)
	}
}

func TestAlphaOperationEnvelopeDecoderQualifiedFixture(t *testing.T) {
	// Consume the approved producer's actual fixture without importing its Go
	// types. This is wire compatibility, never actual daemon qualification.
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "internal", "operation", "testdata", "qualified-success.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Expected json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	result, err := decodeAlphaOperation(fixture.Expected)
	if err != nil {
		t.Fatal(err)
	}
	const attested = "sha256:e8602058ec84f05ce8c9db6c09ab4163243537579c950a40d0e82d724d784a80"
	if result.Target == nil || result.Target.QualifiedIdentityDigest != attested || result.Target.EndpointDigest == attested {
		t.Fatal("fixture confused selector digest and attested daemon identity")
	}
	for _, value := range []any{nil, "", "sha256:", strings.TrimPrefix(attested, "sha256:"), strings.Replace(attested, "sha256:", "sha512:", 1), "sha256:" + strings.Repeat("G", 64), true, 42} {
		var document map[string]any
		if err := json.Unmarshal(fixture.Expected, &document); err != nil {
			t.Fatal(err)
		}
		document["target"].(map[string]any)["qualifiedIdentityDigest"] = value
		bad, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeAlphaOperation(bad); err == nil {
			t.Fatal("accepted malformed/null attested target identity")
		}
	}
	for _, location := range []string{"root", "target"} {
		var document map[string]any
		if err := json.Unmarshal(fixture.Expected, &document); err != nil {
			t.Fatal(err)
		}
		fields := document
		if location == "target" {
			fields = document["target"].(map[string]any)
		}
		fields["unnegotiatedFutureField"] = true
		bad, _ := json.Marshal(document)
		if _, err := decodeAlphaOperation(bad); err == nil {
			t.Fatal("qualified extension broadly admitted unknown fields")
		}
	}
	// Absence is valid for an unqualified target; presence is not Podman support.
	var unqualified map[string]any
	if err := json.Unmarshal(fixture.Expected, &unqualified); err != nil {
		t.Fatal(err)
	}
	delete(unqualified["target"].(map[string]any), "qualifiedIdentityDigest")
	plain, _ := json.Marshal(unqualified)
	if _, err := decodeAlphaOperation(plain); err != nil {
		t.Fatal("optional attested identity became mandatory")
	}
	podman := strings.Replace(string(fixture.Expected), `"engine": "docker"`, `"engine": "podman"`, 1)
	if _, err := decodeAlphaOperation([]byte(podman)); err == nil {
		t.Fatal("attested digest advertised unsupported Podman qualification")
	}
}

func TestAlphaOperationEnvelopeConformance(t *testing.T) {
	if os.Getenv("DILL_TEST_ALPHA_OPERATIONS") != "1" {
		t.Skip("requires explicit disposable operation-contract opt-in")
	}
	env := integrationEnv(t)
	socket := os.Getenv("DILL_ENGINE_SOCKET")
	if !strings.HasPrefix(socket, "unix:///") {
		t.Fatal("operation qualification requires an explicitly pinned Unix URI")
	}
	root := repoRoot(t)
	bin := dillBinary(t, root)
	stack := fmt.Sprintf("dill-alpha-op-%s-%d", env.engine, time.Now().UnixNano())
	cfg := filepath.Join(t.TempDir(), "compose.pkl")
	const sentinel = "ALPHA_SYNTHETIC_OUTPUT_SENTINEL_v1"
	writeConfig(t, cfg, root, env.engine, stack, sentinel)
	cleanupStack(t, bin, cfg, stack)
	seen := map[string]bool{}

	run := func(operation, input string, wantExit int, outcome, effects string, extra ...string) alphaOperationResult {
		t.Helper()
		args := []string{"operation", operation, "--contract", "dill.operation/1", "--engine", env.engine, "--engine-socket", socket, "-p", stack, "-f", cfg}
		args = append(args, extra...)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdin = strings.NewReader(input)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exited *exec.ExitError
			if !errors.As(err, &exited) {
				t.Fatalf("operation process did not return a terminal exit: %T", err)
			}
			code = exited.ExitCode()
		}
		if bytes.Contains(stdout.Bytes(), []byte(sentinel)) || bytes.Contains(stderr.Bytes(), []byte(sentinel)) || bytes.Contains(stdout.Bytes(), []byte("docker.io/library/")) || bytes.Contains(stdout.Bytes(), []byte(root)) {
			t.Fatal("operation output leaked synthetic configuration or evaluator diagnostics")
		}
		result, decodeErr := decodeAlphaOperation(stdout.Bytes())
		if decodeErr != nil {
			t.Fatalf("invalid terminal operation result: %v (exit=%d; raw output withheld)", decodeErr, code)
		}
		if code != wantExit || result.Outcome != outcome || result.Effects != effects {
			t.Fatalf("%s: exit/outcome/effects=%d/%s/%s; want %d/%s/%s", operation, code, result.Outcome, result.Effects, wantExit, outcome, effects)
		}
		if result.Operation != operation || result.Stack != stack || result.Target == nil || result.Target.Engine != env.engine || result.Target.EndpointDigest != alphaDigest(socket) || result.Target.QualifiedIdentityDigest != "" || seen[result.OperationID] {
			t.Fatal("incorrect operation/target/stack correlation or reused operation ID")
		}
		seen[result.OperationID] = true
		for _, resource := range result.Resources {
			if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(resource.IdentityDigest) {
				t.Fatal("resource identity is not an opaque contract digest")
			}
		}
		if wantExit == 0 && outcome == "succeeded" && len(result.Errors) != 0 {
			t.Fatal("successful operation reported errors")
		}
		if wantExit != 0 && outcome != "changes" && len(result.Errors) == 0 {
			t.Fatal("failure/cancellation omitted fixed diagnostics")
		}
		fixedErrors := map[string]string{
			"INVALID_INPUT":    "Invalid operation input.",
			"OPERATION_FAILED": "Operation failed. Inspect the target before retrying if effects are uncertain.",
			"CANCELLED":        "Operation cancelled or deadline exceeded.",
		}
		for _, reported := range result.Errors {
			if message, ok := fixedErrors[reported.Code]; !ok || message != reported.Message {
				t.Fatal("operation emitted diagnostics outside the fixed contract")
			}
		}
		return result
	}

	run("validate", "", 0, "succeeded", "none")
	planned := run("plan", "", 1, "changes", "none")
	if !planned.ChangesDetected || len(planned.Resources) != 1 || planned.Resources[0].Action != "create" {
		t.Fatal("initial plan omitted its create observation")
	}
	run("diff", "", 0, "changes", "none")
	run("up", "", 0, "succeeded", "complete")
	original := strings.TrimSpace(runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app"))
	noop := run("plan", "", 0, "succeeded", "none")
	if noop.ChangesDetected || len(noop.Resources) != 1 || noop.Resources[0].Action != "noop" {
		t.Fatal("repeat plan is not a no-op")
	}
	run("up", "", 0, "succeeded", "complete")
	if after := strings.TrimSpace(runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app")); after != original {
		t.Fatal("repeat up replaced a converged container")
	}
	writeConfig(t, cfg, root, env.engine, stack, sentinel+"_changed")
	changed := run("plan", "", 1, "changes", "none")
	if !changed.ChangesDetected || len(changed.Resources) != 1 || changed.Resources[0].Action != "recreate" {
		t.Fatal("changed config did not report recreate")
	}
	run("up", "", 0, "succeeded", "complete")
	for _, operation := range []string{"stop", "start", "restart", "stop"} {
		run(operation, "", 0, "succeeded", "complete")
	}
	beforeDecline := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app")
	run("rm", "n\n", 0, "cancelled", "none")
	if after := runEngine(t, env.engine, "inspect", "--format", "{{.Id}}", stack+"_app"); after != beforeDecline {
		t.Fatal("declined rm changed container identity")
	}
	run("teardown", "n\n", 0, "cancelled", "none")
	run("rm", "", 0, "succeeded", "complete", "--yes")
	// Retention remains a read-only failure, not a successful network deletion.
	run("down", "", 1, "failed", "none")

	// Invalid whole-graph input must be rejected without any observed mutation.
	body := fmt.Sprintf("amends %q\nconfig { name = %q; engine = %q; services { [\"app\"] { image = \"docker.io/library/busybox:1.36\"; dependsOn = List(\"missing\") } } }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run("up", "", 2, "invalid_input", "none")

	// A failing aggregate create is ambiguous even when rollback removes it.
	body = fmt.Sprintf("amends %q\nconfig { name = %q; engine = %q; services { [\"app\"] { image = \"docker.io/library/busybox:1.36\"; command = List(\"/synthetic-missing-executable\") } } }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	run("up", "", 1, "unknown", "unknown")

	// Readiness deadline after creation: cancellation does not imply no effects.
	body = fmt.Sprintf("amends %q\nconfig { name = %q; engine = %q; services { [\"app\"] { image = \"docker.io/library/busybox:1.36\"; command = List(\"sleep\", \"300\"); healthcheck { test = \"false\"; interval = 1.s; timeout = 1.s; retries = 300 } } } }\n", filepath.ToSlash(filepath.Join(root, "dill.pkl")), stack, env.engine)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled := run("up", "", 1, "cancelled", "partial", "--timeout", "10s")
	if len(cancelled.Resources) == 0 {
		t.Fatal("deadline did not exercise post-mutation cancellation")
	}
}
