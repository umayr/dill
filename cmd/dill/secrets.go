package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/dag"
	"github.com/umayr/dill/internal/log"
	"github.com/umayr/dill/internal/secretfile"
)

const secretCheckTimeout = 10 * time.Second

func captureSecretPolicy(o *commandOptions) {
	if !o.SecretPolicyCaptured {
		o.SecretPolicy = os.Getenv("DILL_SECRET_POLICY")
		o.SecretPolicyCaptured = true
	}
}

const secretCapabilitiesJSON = `{"contract":"dill.alpha-secrets/1","bindings":"dill.secret-bindings/1","policy":"dill.secret-policy/1","engines":["docker-host-file","podman-host-file"]}`
const secretAvailableJSON = `{"contract":"dill.alpha-secrets/1","status":"available"}`
const secretUnavailableJSON = `{"contract":"dill.alpha-secrets/1","status":"unavailable","error":{"code":"SECRET_UNAVAILABLE","message":"Secret configuration or source unavailable."}}`

func secretInvocation(args []string) bool {
	if _, ok := os.LookupEnv("DILL_SECRET_POLICY"); ok {
		return true
	}
	if secretCheckInvocation(args) {
		return true
	}
	for _, a := range args {
		for _, key := range []string{"secret-bindings", "secret-target", "proof-file", "proof-key-file"} {
			if a == "--"+key || a == "-"+key || strings.HasPrefix(a, "--"+key+"=") || strings.HasPrefix(a, "-"+key+"=") {
				return true
			}
		}
	}
	return false
}
func secretCheckInvocation(args []string) bool {
	// Locate the command, not a flag value named "secrets".
	values := map[string]bool{"f": true, "p": true, "stack-name": true, "project-name": true, "engine": true, "engine-socket": true, "env-file": true, "timeout": true, "secret-target": true, "secret-bindings": true, "proof-file": true, "proof-key-file": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return i+1 < len(args) && args[i+1] == "secrets"
		}
		if !strings.HasPrefix(a, "-") {
			return a == "secrets"
		}
		key := strings.TrimLeft(a, "-")
		if !strings.Contains(key, "=") && values[key] {
			i++
		}
	}
	return false
}
func hasSecretGrants(c *config.DillConfig) bool {
	if c != nil {
		for _, s := range c.Services {
			if s != nil && len(s.Secrets) > 0 {
				return true
			}
		}
	}
	return false
}

// One CLI invocation owns the diagnostic scope. Successful evaluation can
// reveal grants after parsing; cleanup still needs fixed errors/log suppression
// without acquiring bindings, policy or source availability.
type secretDiagnostics struct {
	safe    bool
	restore func()
}

func (d *secretDiagnostics) enable() {
	if d != nil && !d.safe {
		d.safe = true
		d.restore = silenceSecretLogs()
	}
}
func (d *secretDiagnostics) close() {
	if d != nil && d.restore != nil {
		d.restore()
	}
}

func (o commandOptions) secretMode() bool {
	return o.SafeSecrets || o.SecretBindingsSet || o.SecretPolicy != "" || o.SecretTarget != "" || (o.secretDiagnostics != nil && o.secretDiagnostics.safe)
}

// CLI execution is single-command scoped. Both Dill and SDK diagnostics are
// silenced before evaluating any secret-enabled input, including debug mode.
func silenceSecretLogs() func() {
	restore := logger.Suppress()
	l := logrus.StandardLogger()
	old := l.Out
	l.SetOutput(io.Discard)
	return func() { l.SetOutput(old); restore() }
}
func fixedSecretError(err error) error {
	if err != nil && !errors.Is(err, errPlanHasChanges) {
		return config.ErrSecretUnavailable
	}
	return err
}
func addSecretBindingsFlag(fs *flag.FlagSet, o *commandOptions) {
	fs.Func("secret-bindings", "absolute private secret binding document", func(v string) error { o.SecretBindings = v; o.SecretBindingsSet = true; return nil })
}

func validateTrustedStack(o commandOptions, c *config.DillConfig) error {
	if c == nil {
		return config.ErrSecretUnavailable
	}
	if (o.secretMode() || hasSecretGrants(c)) && (o.StackNameSet || o.StackName != "") && (o.StackName == "" || (c.Name != "" && c.Name != o.StackName)) {
		return config.ErrSecretUnavailable
	}
	return nil
}

func secretRequest(o commandOptions, c *config.DillConfig, names []string, noDeps, authorizationOnly bool) (secretfile.Request, error) {
	endpoint, endpointErr := localEngineSocket(o)
	socket := strings.TrimPrefix(endpoint, "unix://")
	if endpointErr != nil || c == nil || c.Name == "" || ((o.StackNameSet || o.StackName != "") && o.StackName != c.Name) || !o.EngineSocketSet || o.Engine == "" || o.Engine != c.Engine || !strings.HasPrefix(socket, "/") || path.Clean(socket) != socket || !o.SecretBindingsSet || o.SecretBindings == "" || o.SecretPolicy == "" || o.SecretTarget == "" {
		return secretfile.Request{}, config.ErrSecretUnavailable
	}
	return secretfile.Request{Config: c, PolicyPath: o.SecretPolicy, BindingsPath: o.SecretBindings, Target: o.SecretTarget, Stack: c.Name, Engine: c.Engine, Socket: socket, Services: append([]string(nil), names...), NoDeps: noDeps, AuthorizationOnly: authorizationOnly}, nil
}

var secretPreflight = secretfile.Preflight

func prepareSecrets(ctx context.Context, o commandOptions, c *config.DillConfig, names []string, noDeps, authorizationOnly bool) (secretfile.Request, *secretfile.Result, error) {
	req, err := secretRequest(o, c, names, noDeps, authorizationOnly)
	if err != nil {
		return req, nil, config.ErrSecretUnavailable
	}
	result, err := secretPreflight(ctx, req)
	if err != nil {
		return req, nil, config.ErrSecretUnavailable
	}
	return req, result, nil
}

type discardPullSink struct{}

func (discardPullSink) Write(p []byte) (int, error) { return len(p), nil }
func (discardPullSink) Begin()                      {}
func (discardPullSink) Done()                       {}
func secretPullSink(string) dag.PullSink            { return discardPullSink{} }

func runSecretCapabilities(args []string) error {
	fs := flag.NewFlagSet("secret-capabilities", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "output format: json")
	if fs.Parse(args) != nil || *format != "json" || len(fs.Args()) != 0 {
		return config.ErrSecretUnavailable
	}
	_, err := fmt.Fprintln(os.Stdout, secretCapabilitiesJSON)
	return err
}
func runSecretsCheck(ctx context.Context, o commandOptions, args []string) error {
	fs := flag.NewFlagSet("secrets check", flag.ContinueOnError)
	addConfigFlags(fs, &o)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "json", "output format: json")
	if len(args) == 0 || args[0] != "check" || fs.Parse(args[1:]) != nil || *format != "json" || len(fs.Args()) != 0 {
		return config.ErrSecretUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, secretCheckTimeout)
	defer cancel()
	c, err := loadConfig(ctx, o)
	if err != nil {
		return config.ErrSecretUnavailable
	}
	_, _, err = prepareSecrets(ctx, o, c, nil, false, false)
	return err
}

func requiresSecretPreflight(o commandOptions, c *config.DillConfig) bool {
	return hasSecretGrants(c) || o.SecretBindingsSet
}
func secretCommandOptions(o commandOptions, args []string) (commandOptions, func()) {
	captureSecretPolicy(&o)
	o.SafeSecrets = o.secretMode() || secretInvocation(args)
	if o.SafeSecrets {
		return o, silenceSecretLogs()
	}
	return o, func() {}
}
