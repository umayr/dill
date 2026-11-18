// dill-agent serves the experimental host read protocol.
package main

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
	"strings"
	"syscall"
	"time"

	"github.com/umayr/dill/internal/hostagent"
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
func run() error {
	listen := flag.String("socket", "", "private agent Unix socket")
	httpsListen := flag.String("https-listen", "", "read-only HTTPS listener on a private IP:port (alternative to --socket)")
	httpsCert := flag.String("https-cert-file", "", "HTTPS certificate PEM file")
	httpsKey := flag.String("https-key-file", "", "HTTPS private key PEM file")
	httpsToken := flag.String("https-token-file", "", "owner-only HTTPS token file")
	engine := flag.String("engine-socket", "", "absolute Docker Unix socket path")
	engineID := flag.String("engine-id", "", "enrolled engine UUID")
	daemon := flag.String("daemon-id", "", "enrolled Docker daemon ID")
	generation := flag.Uint64("generation", 0, "enrolled binding generation")
	key := flag.String("key-file", "", "private file containing hex Ed25519 seed")
	revealKey := flag.String("reveal-public-key", "", "optional enrolled Brine Ed25519 public key, hex")
	flag.Parse()
	if flag.NArg() != 0 || !filepath.IsAbs(*engine) || filepath.Clean(*engine) != *engine || *key == "" {
		return errors.New("absolute engine socket path and enrollment flags required")
	}
	if *httpsListen != "" {
		if *listen != "" || *httpsCert == "" || *httpsKey == "" || *httpsToken == "" || *revealKey != "" {
			return errors.New("HTTPS requires certificate, key and token files, without Unix listener or reveal")
		}
	} else {
		if !filepath.IsAbs(*listen) || *httpsCert != "" || *httpsKey != "" || *httpsToken != "" {
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
	f, err := os.Open(*key)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	kst, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || int(kst.Uid) != os.Geteuid() || info.Size() > 256 {
		return errors.New("key file must be private and owned by agent user")
	}
	var seedText string
	if _, err = fmt.Fscan(f, &seedText); err != nil {
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
		return serveHTTPS(*httpsListen, *httpsCert, *httpsKey, *httpsToken, server)
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
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dill-agent:", strings.ReplaceAll(err.Error(), "\n", " "))
		os.Exit(1)
	}
}
