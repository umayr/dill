package agentcmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/hostagent"
)

func TestHTTPSProvisionAndExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credentials")
	if err := initHTTPS([]string{"--dir", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := checkHTTPSDir(dir); err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(dir, "session-seed")
	certPath := filepath.Join(dir, "tls-cert.pem")
	keyPath := filepath.Join(dir, "tls-key.pem")
	tokenPath := filepath.Join(dir, "token")
	for _, name := range []string{seedPath, certPath, keyPath, tokenPath} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("insecure or missing credential file: %s", filepath.Base(name))
		}
	}
	seedText, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(seedText)))
	if err != nil || len(seed) != 32 {
		t.Fatal("invalid generated seed")
	}
	cfg := hostagent.Config{
		PrivateKey: ed25519.NewKeyFromSeed(seed), EngineID: "11111111-1111-4111-8111-111111111111",
		Generation: 1, EngineKind: "docker", EngineSocket: "unix:///run/docker.sock", DaemonID: "synthetic-daemon",
	}
	var before, after bytes.Buffer
	export := func(out *bytes.Buffer) {
		t.Helper()
		if err := exportPairing(out, "127.0.0.1:9211", certPath, keyPath, tokenPath, cfg); err != nil {
			t.Fatal(err)
		}
	}
	export(&before)
	if err := initHTTPS([]string{"--dir", dir}, io.Discard); err == nil {
		t.Fatal("overwrote existing pairing")
	}
	export(&after)
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("regeneration changed enrolled credentials")
	}
	var fields map[string]json.RawMessage
	var bundle pairingBundle
	if json.Unmarshal(before.Bytes(), &fields) != nil || json.Unmarshal(before.Bytes(), &bundle) != nil {
		t.Fatal("invalid pairing output")
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wantKeys := []string{"agentPublicKey", "certificateSha256", "daemonId", "engineId", "engineKind", "engineSocket", "generation", "token", "version"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("wrong pairing fields: %v", keys)
	}
	cert, token, err := httpsCredentials("127.0.0.1:9211", certPath, keyPath, tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(cert.Certificate[0])
	if bundle.Version != 1 || bundle.Token != token || bundle.CertificateSHA256 != hex.EncodeToString(pin[:]) ||
		bundle.AgentPublicKey != hex.EncodeToString(cfg.PrivateKey.Public().(ed25519.PublicKey)) || bundle.EngineID != cfg.EngineID ||
		bundle.Generation != cfg.Generation || bundle.EngineKind != cfg.EngineKind || bundle.EngineSocket != cfg.EngineSocket || bundle.DaemonID != cfg.DaemonID {
		t.Fatal("pairing bundle does not bind the generated credentials and configured target")
	}
	deployFile := filepath.Join(t.TempDir(), "deploy-token")
	deployToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))
	if err := os.WriteFile(deployFile, []byte(deployToken+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var withDeploy bytes.Buffer
	args := []string{"--https-dir", dir, "--https-listen", "127.0.0.1:9211", "--engine-socket", "/run/docker.sock",
		"--engine-id", cfg.EngineID, "--daemon-id", cfg.DaemonID, "--generation", "1", "--https-deploy-token-file", deployFile,
		"--https-staging-root", "/test/stage", "--https-staging-catalog", "/test/catalog", "--https-deploy-grant", "stage", "--print-https-pairing"}
	if err := serveAgent(args, &withDeploy, io.Discard, false); err != nil || !bytes.Equal(before.Bytes(), withDeploy.Bytes()) {
		t.Fatal("deploy configuration changed v1 monitor pairing output", err)
	}
	if err := os.Chmod(tokenPath, 0644); err != nil {
		t.Fatal(err)
	}
	var denied bytes.Buffer
	if err := exportPairing(&denied, "127.0.0.1:9211", certPath, keyPath, tokenPath, cfg); err == nil || denied.Len() != 0 {
		t.Fatal("exported an insecure credential")
	}
}

func TestHTTPSInvalidDeployConfigFailsBeforeStaging(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "credentials")
	if err := initHTTPS([]string{"--dir", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	args := []string{"--https-dir", dir, "--https-listen", "127.0.0.1:9211", "--engine-socket", "/run/docker.sock",
		"--engine-id", "11111111-1111-4111-8111-111111111111", "--daemon-id", "synthetic-daemon", "--generation", "1",
		"--https-staging-root", filepath.Join(root, "absent-stage"), "--https-staging-catalog", filepath.Join(root, "absent-catalog")}
	deploy := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32)) + "\n"
	makeFile := func(name, value string, perm os.FileMode) string {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(value), perm); err != nil {
			t.Fatal(err)
		}
		return p
	}
	valid := makeFile("valid", deploy, 0600)
	public := makeFile("public", deploy, 0644)
	malformed := makeFile("malformed", "not-canonical", 0600)
	monitor := filepath.Join(dir, "token")
	monitorBytes, err := os.ReadFile(monitor)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := makeFile("duplicate", string(monitorBytes), 0600)
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, grant, tokenFile, want string
	}{
		{"missing", "stage", "", "require --https-deploy-token-file"},
		{"monitor file", "stage", monitor, "must differ from the monitor token"},
		{"duplicated contents", "stage", duplicate, "must differ from the monitor token"},
		{"public", "stage", public, "must be an owner-only regular file"},
		{"malformed", "stage", malformed, "must be an owner-only regular file"},
		{"symlink", "stage", symlink, "must be an owner-only regular file"},
		{"relative", "stage", "relative", "must be an absolute path"},
		{"unknown grant", "*", valid, "unknown HTTPS deploy grant"},
		{"monitor is not a deploy grant", "monitor", valid, "unknown HTTPS deploy grant"},
		{"valid token and grant reach existing staging checks", "stage", valid, "cannot initialize private HTTPS snapshot staging"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			argv := append(append([]string(nil), args...), "--https-deploy-grant", tt.grant, "--https-deploy-token-file", tt.tokenFile)
			if err := serveAgent(argv, io.Discard, io.Discard, false); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}
