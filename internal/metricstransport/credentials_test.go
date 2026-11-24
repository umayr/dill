//go:build darwin || linux

package metricstransport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func hooksOK() Hooks {
	return Hooks{
		AcceptsText:   func(http.Header) bool { return true },
		AcquireRender: func(context.Context, string) ([]byte, error) { return []byte(testMetrics), nil },
	}
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal("cannot write synthetic fixture")
	}
}

func certConfig(t *testing.T, editCA, editLeaf func(*x509.Certificate)) Config {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("cannot canonicalize test directory")
	}
	cfg := Config{
		Enabled: true, BindAddress: "192.168.40.21:9443", ServerName: "scrape.invalid", ExpectedDaemonID: "synthetic-daemon",
		CAFile: filepath.Join(dir, "ca.pem"), CertFile: filepath.Join(dir, "cert.pem"),
		KeyFile: filepath.Join(dir, "key.pem"), TokenFile: filepath.Join(dir, "token"),
	}
	now := time.Now()
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	intermediate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Synthetic Intermediate"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: cfg.ServerName},
		DNSNames: []string{cfg.ServerName}, IPAddresses: []net.IP{net.ParseIP("192.168.40.21")},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if editCA != nil {
		editCA(root)
	}
	if editLeaf != nil {
		editLeaf(leaf)
	}
	rootPEM, rootKey := issueCertificate(t, root, root, nil)
	intermediatePEM, intermediateKey := issueCertificate(t, intermediate, root, rootKey)
	leafPEM, leafKey := issueCertificate(t, leaf, intermediate, intermediateKey)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal("cannot marshal synthetic key")
	}
	writeFixture(t, cfg.CAFile, rootPEM)
	writeFixture(t, cfg.CertFile, append(leafPEM, intermediatePEM...))
	writeFixture(t, cfg.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	writeFixture(t, cfg.TokenFile, []byte(syntheticToken(1)+"\n"))
	return cfg
}

func issueCertificate(t *testing.T, template, parent *x509.Certificate, signer ed25519.PrivateKey) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("cannot generate synthetic signing fixture")
	}
	if signer == nil {
		signer = key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	if err != nil {
		t.Fatal("cannot issue synthetic certificate")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key
}

func TestPrepareUnstartedAndOptIn(t *testing.T) {
	if s, err := Prepare(Config{BindAddress: "unusable", TokenFile: "missing"}, Hooks{}); s != nil || err != nil {
		t.Fatal("disabled config read or validated inputs")
	}
	cfg := certConfig(t, nil, nil)
	if s, err := Prepare(cfg, Hooks{}); s != nil || err == nil {
		t.Fatal("missing hooks must fail closed")
	}
	cfg.ReservedAddresses = []string{"127.0.0.1:9444", "192.168.40.22:9443"}
	reserved := filepath.Join(filepath.Dir(cfg.TokenFile), "unrelated")
	if err := os.WriteFile(reserved, nil, 0000); err != nil {
		t.Fatal("cannot create metadata-only fixture")
	}
	cfg.ReservedCredentialFiles = []string{reserved}
	server, err := Prepare(cfg, hooksOK())
	if err != nil {
		t.Fatal(err)
	}
	if server.Addr != cfg.BindAddress || server.TLSConfig.MinVersion != tls.VersionTLS13 || server.TLSConfig.ClientAuth != tls.NoClientCert ||
		!server.TLSConfig.SessionTicketsDisabled || len(server.TLSConfig.Certificates) != 1 ||
		!server.Protocols.HTTP1() || server.Protocols.HTTP2() || server.Protocols.UnencryptedHTTP2() ||
		server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatal("unexpected insecure or unbounded server configuration")
	}
	if !server.DisableGeneralOptionsHandler {
		t.Fatal("OPTIONS * must be dispatched to the scrape handler")
	}
	r := scrapeRequest()
	r.Method, r.RequestURI, r.URL.Path = http.MethodOptions, "*", "*"
	r.Header.Del("Authorization")
	if w := record(server.Handler, r); w.Code != http.StatusNotFound || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("OPTIONS *: status %d, Cache-Control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := record(server.Handler, scrapeRequest()); w.Code != 200 {
		t.Fatalf("unstarted server handler status: %d", w.Code)
	}
	cfg.ServerName = "192.168.40.21"
	if _, err := Prepare(cfg, hooksOK()); err != nil {
		t.Fatalf("matching IP SAN rejected: %v", err)
	}
}

func TestConfigRejectsUnsafeEndpoints(t *testing.T) {
	for _, addr := range []string{
		":9443", "0.0.0.0:9443", "[::]:9443", "[::ffff:0.0.0.0]:9443", "8.8.8.8:9443", "[2001:4860::1]:9443",
		"169.254.1.1:9443", "[fe80::1%eth0]:9443", "[fd42::1%eth0]:9443", "localhost:9443", "127.0.0.1:0", "127.0.0.1",
	} {
		t.Run(addr, func(t *testing.T) {
			cfg := Config{BindAddress: addr}
			if _, err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "explicit private or loopback") {
				t.Fatal("unsafe bind did not fail")
			}
		})
	}
	base := Config{BindAddress: "127.0.0.1:9443", ServerName: "scrape.invalid", ExpectedDaemonID: "opaque",
		CAFile: "/ca", CertFile: "/cert", KeyFile: "/key", TokenFile: "/token"}
	for _, addr := range []string{"127.0.0.1:9443", "[::ffff:127.0.0.1]:9443", "[::]:9443", "0.0.0.0:9443", ":9443", "hostname:9443"} {
		cfg := base
		cfg.ReservedAddresses = []string{addr}
		if _, err := validateConfig(cfg); err == nil {
			t.Fatalf("unsafe reserved endpoint not rejected: %s", addr)
		}
	}
	for input, want := range map[string]string{"[::ffff:127.0.0.1]:9443": "127.0.0.1:9443", "[fd42::1]:9443": "[fd42::1]:9443", "10.0.0.1:9443": "10.0.0.1:9443"} {
		cfg := base
		cfg.BindAddress = input
		if got, err := validateConfig(cfg); err != nil || got != want {
			t.Errorf("private bind validation = %s, %v; want %s", got, err, want)
		}
	}
	for _, edit := range []func(*Config){
		func(c *Config) { c.ServerName = "*.invalid" },
		func(c *Config) { c.ExpectedDaemonID = "" },
		func(c *Config) { c.ExpectedDaemonID = "with spaces" },
		func(c *Config) { c.TokenFile = c.KeyFile },
		func(c *Config) { c.TokenFile = "/subdir/../token" },
		func(c *Config) { c.TokenFile = "relative-token" },
		func(c *Config) { c.ReservedCredentialFiles = []string{c.TokenFile} },
	} {
		cfg := base
		edit(&cfg)
		if _, err := validateConfig(cfg); err == nil {
			t.Error("unsafe config unexpectedly accepted")
		}
	}
}

func TestInvalidTLSCredentials(t *testing.T) {
	for _, tt := range []struct {
		name     string
		editCA   func(*x509.Certificate)
		editLeaf func(*x509.Certificate)
		edit     func(*testing.T, *Config)
	}{
		{name: "DNS mismatch", edit: func(_ *testing.T, c *Config) { c.ServerName = "unrelated.invalid" }},
		{name: "IP mismatch", edit: func(_ *testing.T, c *Config) { c.ServerName = "192.168.40.22" }},
		{name: "common name alone", editLeaf: func(c *x509.Certificate) { c.DNSNames, c.IPAddresses = nil, nil }},
		{name: "expired leaf", editLeaf: func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) }},
		{name: "expired CA", editCA: func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) }},
		{name: "not valid yet", editLeaf: func(c *x509.Certificate) { c.NotBefore = time.Now().Add(time.Minute) }},
		{name: "client cert only", editLeaf: func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }},
		{name: "no signing usage", editLeaf: func(c *x509.Certificate) { c.KeyUsage = x509.KeyUsageKeyEncipherment }},
		{name: "untrusted CA", edit: func(t *testing.T, c *Config) { c.CAFile = certConfig(t, nil, nil).CAFile }},
		{name: "mismatched key", edit: func(t *testing.T, c *Config) { c.KeyFile = certConfig(t, nil, nil).KeyFile }},
		{name: "duplicate key blocks", edit: func(t *testing.T, c *Config) {
			data, err := os.ReadFile(c.KeyFile)
			if err != nil {
				t.Fatal("cannot read synthetic fixture")
			}
			writeFixture(t, c.KeyFile, append(data, data...))
		}},
		{name: "missing intermediate", edit: func(t *testing.T, c *Config) {
			data, err := os.ReadFile(c.CertFile)
			if err != nil {
				t.Fatal("cannot read synthetic fixture")
			}
			leaf, _ := pem.Decode(data)
			writeFixture(t, c.CertFile, pem.EncodeToMemory(leaf))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := certConfig(t, tt.editCA, tt.editLeaf)
			if tt.edit != nil {
				tt.edit(t, &cfg)
			}
			if server, err := Prepare(cfg, hooksOK()); server != nil || err == nil {
				t.Fatal("invalid TLS credentials accepted")
			}
		})
	}
}

func TestTokenProtectionAndRotation(t *testing.T) {
	cfg := certConfig(t, nil, nil)
	first, err := Prepare(cfg, hooksOK())
	if err != nil {
		t.Fatal(err)
	}
	if w := record(first.Handler, scrapeRequest()); w.Code != 200 {
		t.Fatalf("original token status: %d", w.Code)
	}
	// Simulate atomic replacement plus restart; the first handler is no longer used.
	replacement := cfg.TokenFile + ".new"
	writeFixture(t, replacement, []byte(syntheticToken(2)))
	if err := os.Rename(replacement, cfg.TokenFile); err != nil {
		t.Fatal("cannot replace synthetic token")
	}
	second, err := Prepare(cfg, hooksOK())
	if err != nil {
		t.Fatal(err)
	}
	if w := record(second.Handler, scrapeRequest()); w.Code != 401 {
		t.Fatalf("old token recognized by replacement: %d", w.Code)
	}
	r := scrapeRequest()
	r.Header.Set("Authorization", "Bearer "+syntheticToken(2))
	if w := record(second.Handler, r); w.Code != 200 {
		t.Fatalf("replacement token rejected: %d", w.Code)
	}
	for _, value := range []string{
		strings.TrimPrefix(syntheticToken(1), tokenPrefix), syntheticToken(1) + "\n\n", syntheticToken(1) + "\r\n",
		syntheticToken(1)[:tokenLength-1] + "B", syntheticToken(1) + "=", tokenPrefix + strings.Repeat("!", 43),
	} {
		writeFixture(t, cfg.TokenFile, []byte(value))
		if server, err := Prepare(cfg, hooksOK()); server != nil || err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("invalid token format was accepted or exposed")
		}
	}
}

func TestInsecureCredentialPaths(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*testing.T, *Config) error
	}{
		{"public mode", func(_ *testing.T, c *Config) error { return os.Chmod(c.TokenFile, 0644) }},
		{"symlink file", func(_ *testing.T, c *Config) error {
			src := c.TokenFile
			c.TokenFile += ".link"
			return os.Symlink(src, c.TokenFile)
		}},
		{"hardlinked file", func(_ *testing.T, c *Config) error { return os.Link(c.TokenFile, c.TokenFile+".link") }},
		{"reserved symlink alias", func(_ *testing.T, c *Config) error {
			alias := c.TokenFile + ".link"
			c.ReservedCredentialFiles = []string{alias}
			return os.Symlink(c.TokenFile, alias)
		}},
		{"FIFO", func(_ *testing.T, c *Config) error {
			c.TokenFile += ".fifo"
			return unix.Mkfifo(c.TokenFile, 0600)
		}},
		{"symlink parent", func(_ *testing.T, c *Config) error {
			dir := filepath.Dir(c.TokenFile)
			alias := filepath.Join(dir, "alias")
			c.TokenFile = filepath.Join(alias, filepath.Base(c.TokenFile))
			return os.Symlink(dir, alias)
		}},
		{"writable parent", func(_ *testing.T, c *Config) error { return os.Chmod(filepath.Dir(c.TokenFile), 0777) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := certConfig(t, nil, nil)
			if err := tt.edit(t, &cfg); err != nil {
				t.Fatal("cannot set up path fixture")
			}
			if server, err := Prepare(cfg, hooksOK()); server != nil || err == nil || strings.Contains(err.Error(), cfg.TokenFile) {
				t.Fatal("unsafe credential path accepted or exposed")
			}
		})
	}
}
