// Package agentcmd runs Dill's experimental host agent from either entrypoint.
package agentcmd

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/coordination"
	"github.com/umayr/dill/internal/hostagent"
	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/serve"
)

type dockerIdentity struct{ client *http.Client }

func (d dockerIdentity) DaemonID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://engine/info", nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("engine unavailable")
	}
	var v struct {
		ID string `json:"ID"`
	}
	err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&v)
	return v.ID, err
}
func (d dockerIdentity) RevealInspect(ctx context.Context, id string) (hostagent.RevealContainer, error) {
	var out hostagent.RevealContainer
	req, err := http.NewRequestWithContext(ctx, "GET", "http://engine/containers/"+id+"/json", nil)
	if err != nil {
		return out, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return out, hostagent.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return out, errors.New("engine unavailable")
	}
	var v struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
			Env    []string          `json:"Env"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(&v); err != nil {
		return out, err
	}
	return hostagent.RevealContainer{ID: v.ID, Labels: v.Config.Labels, Env: v.Config.Env}, nil
}
func (d dockerIdentity) InventoryList(ctx context.Context) ([]hostagent.InventoryContainer, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://engine/containers/json?all=1", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("engine unavailable")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20))
	limitErr := func(e error) error {
		var max *http.MaxBytesError
		if errors.As(e, &max) {
			return hostagent.ErrLimit
		}
		return e
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, limitErr(err)
	}
	if tok != json.Delim('[') {
		return nil, errors.New("invalid engine response")
	}
	out := make([]hostagent.InventoryContainer, 0, 1000)
	for dec.More() {
		if len(out) == 1000 {
			return nil, hostagent.ErrLimit
		}
		var row struct {
			ID     string            `json:"Id"`
			Labels map[string]string `json:"Labels"`
		}
		if err = dec.Decode(&row); err != nil {
			return nil, limitErr(err)
		}
		out = append(out, hostagent.InventoryContainer{ID: row.ID, Labels: row.Labels})
	}
	tok, err = dec.Token()
	if err != nil {
		return nil, limitErr(err)
	}
	if tok != json.Delim(']') {
		return nil, errors.New("invalid engine response")
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("invalid engine response")
		}
		return nil, limitErr(err)
	}
	return out, nil
}
func (d dockerIdentity) InventoryInspect(ctx context.Context, id string) (hostagent.InventoryContainer, error) {
	var out hostagent.InventoryContainer
	req, err := http.NewRequestWithContext(ctx, "GET", "http://engine/containers/"+id+"/json", nil)
	if err != nil {
		return out, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return out, hostagent.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return out, errors.New("engine unavailable")
	}
	var v struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(&v); err != nil {
		return out, err
	}
	return hostagent.InventoryContainer{ID: v.ID, Name: strings.TrimPrefix(v.Name, "/"), State: v.State.Status, Image: v.Config.Image, Labels: v.Config.Labels}, nil
}
func serveAgent(args []string, out, diagnostics io.Writer, combined bool) error {
	fs := flag.NewFlagSet("dill agent", flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	listen := fs.String("socket", "", "private agent Unix socket")
	httpsListen := fs.String("https-listen", "", "HTTPS listener on a private IP:port, read-only by default (alternative to --socket)")
	httpsDir := fs.String("https-dir", "", "absolute monitor credential directory created by init-https")
	httpsCert := fs.String("https-cert-file", "", "HTTPS certificate PEM file")
	httpsKey := fs.String("https-key-file", "", "HTTPS private key PEM file")
	httpsToken := fs.String("https-token-file", "", "owner-only HTTPS monitor token file")
	httpsDeployToken := fs.String("https-deploy-token-file", "", "separately provisioned absolute owner-only HTTPS deploy token file; required for staging and deployment")
	deployGrants := make(httpsDeployGrants)
	fs.Func("https-deploy-grant", "enable one HTTPS deploy operation (repeatable; none by default)", deployGrants.add)
	printPairing := fs.Bool("print-https-pairing", false, "print secret v1 HTTPS monitor pairing bundle to stdout and exit without listening")
	stagingRoot := fs.String("https-staging-root", "", "opt-in private snapshot staging root (no deployment)")
	stagingCatalog := fs.String("https-staging-catalog", "", "private snapshot catalog root; required with staging")
	previewState := fs.String("https-preview-state", "", "opt-in trusted-project deployment preview with this shared native Docker coordination state")
	previewPkl := fs.String("https-preview-pkl", "", "absolute operator-managed Pkl executable for deployment preview")
	applyState := fs.String("https-apply-state", "", "opt-in checked stack creation; separate private operation history directory")
	brokerProfile := fs.String("https-broker-profile", "", "private broker operator profile bound to the existing Docker monitor enrollment")
	brokerStore := fs.String("https-broker-store", "", "opt-in existing qualified Linux broker generation store; requires checked apply and explicit broker deploy grants")
	engine := fs.String("engine-socket", "", "absolute Docker Unix socket path")
	engineID := fs.String("engine-id", "", "enrolled engine UUID")
	daemon := fs.String("daemon-id", "", "enrolled Docker daemon ID")
	generation := fs.Uint64("generation", 0, "enrolled binding generation")
	key := fs.String("key-file", "", "private file containing hex Ed25519 seed")
	revealKey := fs.String("reveal-public-key", "", "optional enrolled Brine Ed25519 public key, hex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*stagingRoot == "") != (*stagingCatalog == "") || (*stagingRoot != "" && *httpsListen == "") {
		return errors.New("snapshot staging requires HTTPS and both private staging and catalog roots")
	}
	if len(deployGrants) != 0 && *stagingRoot == "" {
		return errors.New("HTTPS deploy grants require HTTPS staging configuration")
	}
	var previewExecutable string
	if *previewState != "" || *previewPkl != "" {
		if !combined || runtime.GOOS != "linux" || *stagingRoot == "" || !filepath.IsAbs(*previewState) || !filepath.IsAbs(*previewPkl) {
			return errors.New("deployment preview requires dill agent on Linux, HTTPS staging, an absolute coordination state directory and an absolute Pkl executable")
		}
		executable, err := os.Executable()
		if err != nil || !filepath.IsAbs(executable) {
			return errors.New("Dill executable path unavailable")
		}
		previewExecutable = executable
	}
	if *applyState != "" && (previewExecutable == "" || !filepath.IsAbs(*applyState) || filepath.Clean(*applyState) != *applyState ||
		*applyState == *previewState || *applyState == *stagingRoot || *applyState == *stagingCatalog || *applyState == *httpsDir) {
		return errors.New("deployment apply requires preview and a separate absolute private operation history directory")
	}
	hasBrokerGrant := false
	for name := range deployGrants {
		if strings.HasPrefix(name, "broker-") {
			hasBrokerGrant = true
		}
	}
	if hasBrokerGrant && *brokerStore == "" {
		return errors.New("broker grants require --https-broker-store")
	}
	var brokerProfileTarget, brokerProfileEndpoint string
	if *brokerProfile != "" {
		if *brokerStore == "" {
			return errors.New("broker profile requires --https-broker-store")
		}
		target, endpoint, err := httpsBrokerProfile(*brokerProfile, *engine, *daemon)
		if err != nil {
			return err
		}
		brokerProfileTarget, brokerProfileEndpoint = target, endpoint
	}
	if *brokerStore != "" {
		if *applyState == "" || previewExecutable == "" || !filepath.IsAbs(*brokerStore) || filepath.Clean(*brokerStore) != *brokerStore {
			return errors.New("broker delivery requires Linux combined Dill, checked apply and an absolute existing broker store")
		}
		for _, p := range []string{*applyState, *previewState, *stagingRoot, *stagingCatalog, *httpsDir} {
			if p != "" && (*brokerStore == p || strings.HasPrefix(*brokerStore, p+string(os.PathSeparator)) || strings.HasPrefix(p, *brokerStore+string(os.PathSeparator))) {
				return errors.New("broker runtime store must be separate from other agent state")
			}
		}
		if _, err := readPrivateHTTPSFile(filepath.Join(*applyState, "operations.json"), historyByteLimit); err != nil {
			return errors.New("broker delivery requires an existing deployment journal; missing state cannot be recreated")
		}
		store, err := brokersecret.Open(*brokerStore)
		if err != nil {
			return errors.New("broker runtime store unavailable; provision explicitly, never reset used state")
		}
		if store.Close() != nil {
			return errors.New("broker runtime store release failed")
		}
	}
	if *httpsDir != "" {
		if *httpsListen == "" || *httpsCert != "" || *httpsKey != "" || *httpsToken != "" || *key != "" {
			return errors.New("--https-dir requires --https-listen and cannot be mixed with individual credential files")
		}
		if err := checkHTTPSDir(*httpsDir); err != nil {
			return err
		}
		*httpsCert = filepath.Join(*httpsDir, "tls-cert.pem")
		*httpsKey = filepath.Join(*httpsDir, "tls-key.pem")
		*httpsToken = filepath.Join(*httpsDir, "token")
		*key = filepath.Join(*httpsDir, "session-seed")
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*engine) || filepath.Clean(*engine) != *engine || *key == "" {
		return errors.New("absolute engine socket path and enrollment flags required")
	}
	if *httpsListen != "" {
		if *listen != "" || *httpsCert == "" || *httpsKey == "" || *httpsToken == "" || *revealKey != "" {
			return errors.New("HTTPS requires certificate, key and token files, without Unix listener or reveal")
		}
	} else {
		if !filepath.IsAbs(*listen) || *httpsCert != "" || *httpsKey != "" || *httpsToken != "" || *httpsDeployToken != "" || *printPairing {
			return errors.New("absolute Unix socket path required; HTTPS files require an HTTPS listener")
		}
		parent, err := os.Lstat(filepath.Dir(*listen))
		if err != nil {
			return err
		}
		st, ok := parent.Sys().(*syscall.Stat_t)
		if !ok || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 || int(st.Uid) != os.Geteuid() {
			return errors.New("socket directory must be private and owned by agent user")
		}
	}
	seedContent, err := readPrivateHTTPSFile(*key, 256)
	if err != nil {
		return errors.New("session key file must be private and owned by agent user")
	}
	var seedText string
	if _, err = fmt.Sscan(string(seedContent), &seedText); err != nil {
		return err
	}
	seed, err := hex.DecodeString(seedText)
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("invalid key seed")
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *engine)
	}}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cfg := hostagent.Config{EngineID: *engineID, Generation: *generation, EngineKind: "docker", EngineSocket: "unix://" + *engine, DaemonID: *daemon, PrivateKey: ed25519.NewKeyFromSeed(seed), Identity: dockerIdentity{client}, InventoryBackend: dockerIdentity{client}, InspectBackend: dockerIdentity{client}, LogsBackend: dockerIdentity{client}}
	if *revealKey != "" {
		pub, err := hex.DecodeString(*revealKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("invalid reveal public key")
		}
		cfg.RevealKey = ed25519.PublicKey(pub)
		cfg.RevealBackend = dockerIdentity{client}
	}
	server, err := hostagent.New(cfg)
	if err != nil {
		return err
	}
	if *httpsListen != "" {
		if *printPairing {
			return exportPairing(out, *httpsListen, *httpsCert, *httpsKey, *httpsToken, cfg)
		}
		cert, monitorToken, err := httpsCredentials(*httpsListen, *httpsCert, *httpsKey, *httpsToken)
		if err != nil {
			return err
		}
		deployToken, err := readHTTPSDeployToken(*httpsDeployToken, monitorToken, *stagingRoot != "")
		if err != nil {
			return err
		}
		var staging, preview, apply http.Handler
		if len(deployGrants) != 0 {
			stager, err := serve.NewCataloguedStager(*stagingRoot, *stagingCatalog)
			if err != nil {
				return errors.New("cannot initialize private HTTPS snapshot staging")
			}
			defer stager.Close()
			staging = serve.API{Stager: stager}
			if previewExecutable != "" {
				socket := "unix://" + *engine
				backend := previewEndpoint{stager, cliPreview(previewExecutable, *previewPkl, socket, *daemon, *previewState)}
				preview = backend
				if *applyState != "" {
					history, err := openDeploymentHistory(*applyState, operation.Digest("dill-agent-deployment/1\x00"+socket+"\x00"+*daemon+"\x00"+*engineID))
					if err != nil {
						return err
					}
					defer history.dir.Close()
					managed := &applyEndpoint{previewEndpoint: backend, history: history,
						down: cliPreviewFor("down", previewExecutable, *previewPkl, socket, *daemon, *previewState), apply: cliApply(previewExecutable, *previewPkl, socket, *daemon, *previewState), slot: make(chan struct{}, 1)}
					if *brokerStore != "" {
						id, err := coordination.DockerIdentity(*daemon)
						if err != nil {
							return err
						}
						cli := brokerCLI{exe: previewExecutable, pkl: *previewPkl, socket: *engine, daemon: *daemon, state: *previewState, store: *brokerStore, profile: *brokerProfile, engine: "docker"}
						managed.broker = &brokerEndpoint{stages: stager, history: history, slot: managed.slot, target: "sha256:" + id, endpoint: brokersecret.Digest([]byte(socket)), requirements: cli.requirements, prepare: cli.prepare, retire: cli.retire, review: cli.review, apply: cli.apply}
						if *brokerProfile != "" {
							managed.broker.target = brokerProfileTarget
							managed.broker.endpoint = brokerProfileEndpoint
							managed.broker.engine = "docker"
						}
					}
					apply = managed
				}
			}
		}
		return serveHTTPS(*httpsListen, cert, authenticatedHTTPS(server, staging, preview, apply, monitorToken, deployToken, deployGrants))
	}
	// Never remove an existing path, including a stale socket or symlink.
	if _, err = os.Lstat(*listen); !os.IsNotExist(err) {
		return errors.New("agent socket path already exists or cannot be checked")
	}
	l, err := net.Listen("unix", *listen)
	if err != nil {
		return err
	}
	defer l.Close()
	if err = os.Chmod(*listen, 0600); err != nil {
		return err
	}
	return (&http.Server{Handler: server, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}).Serve(newLimitedListener(l, 32))
}

// Main runs the agent CLI without changing process-wide flags or exiting.
func Main(args []string, out, diagnostics io.Writer, combined bool) int {
	var err error
	switch {
	case len(args) > 0 && args[0] == "init-https":
		err = initHTTPS(args[1:], diagnostics)
	case len(args) > 0 && args[0] == "journal-status":
		err = runJournalStatus(args[1:], out, diagnostics)
	case len(args) > 0 && args[0] == "journal-recovery":
		err = runJournalRecovery(args[1:], out, diagnostics)
	default:
		err = serveAgent(args, out, diagnostics, combined)
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "dill-agent:", strings.ReplaceAll(err.Error(), "\n", " "))
		return 1
	}
	return 0
}
