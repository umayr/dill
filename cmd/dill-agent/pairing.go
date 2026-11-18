package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/umayr/dill/internal/hostagent"
)

// initHTTPS creates a new, owner-only credential directory. On an
// interrupted or failed write it leaves the directory for inspection and will
// not overwrite it on retry. The service cannot start with missing material.
func initHTTPS(args []string) error {
	fs := flag.NewFlagSet("init-https", flag.ContinueOnError)
	dir := fs.String("dir", "", "new absolute credential directory under a trusted parent")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*dir) || filepath.Clean(*dir) != *dir {
		return errors.New("init-https requires a new absolute credential directory")
	}
	pub, pk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return errors.New("cannot generate HTTPS key")
	}
	seed, token := make([]byte, ed25519.SeedSize), make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return errors.New("cannot generate session key")
	}
	if _, err := rand.Read(token); err != nil {
		return errors.New("cannot generate HTTPS token")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil || serial.Sign() == 0 {
		return errors.New("cannot generate HTTPS certificate serial")
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "Dill host agent"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(5, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, pk)
	if err != nil {
		return errors.New("cannot generate HTTPS certificate")
	}
	pkDER, err := x509.MarshalPKCS8PrivateKey(pk)
	if err != nil {
		return errors.New("cannot encode HTTPS key")
	}
	if err := os.Mkdir(*dir, 0700); err != nil {
		return errors.New("cannot create new HTTPS credential directory; existing material is never replaced")
	}
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"tls-cert.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})},
		{"tls-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkDER})},
		{"session-seed", []byte(hex.EncodeToString(seed) + "\n")},
		{"token", []byte(base64.RawURLEncoding.EncodeToString(token) + "\n")},
	} {
		p, err := os.OpenFile(filepath.Join(*dir, f.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return errors.New("cannot create HTTPS credential file; inspect the new directory")
		}
		_, writeErr := p.Write(f.data)
		syncErr := p.Sync()
		closeErr := p.Close()
		if errors.Join(writeErr, syncErr, closeErr) != nil {
			return errors.New("cannot persist HTTPS credentials; inspect the new directory")
		}
	}
	for _, name := range []string{*dir, filepath.Dir(*dir)} {
		d, err := os.Open(name)
		if err != nil {
			return errors.New("cannot verify credential directory durability")
		}
		if errors.Join(d.Sync(), d.Close()) != nil {
			return errors.New("cannot verify credential directory durability")
		}
	}
	return nil
}

// The caller controls this directory and its ancestors. These checks do not
// protect against concurrent changes by a trusted local user or administrator.
func checkHTTPSDir(path string) error {
	denied := errors.New("HTTPS credential directory must be absolute, owner-only and not a symlink")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return denied
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return denied
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() {
		return denied
	}
	return nil
}

type pairingBundle struct {
	Version           int    `json:"version"`
	CertificateSHA256 string `json:"certificateSha256"`
	Token             string `json:"token"`
	AgentPublicKey    string `json:"agentPublicKey"`
	EngineID          string `json:"engineId"`
	Generation        uint64 `json:"generation"`
	EngineKind        string `json:"engineKind"`
	EngineSocket      string `json:"engineSocket"`
	DaemonID          string `json:"daemonId"`
}

func exportPairing(w io.Writer, address, certFile, keyFile, tokenFile string, cfg hostagent.Config) error {
	cert, token, err := httpsCredentials(address, certFile, keyFile, tokenFile)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return errors.New("HTTPS leaf certificate is invalid or outside its validity period")
	}
	pin := sha256.Sum256(leaf.Raw)
	bundle := pairingBundle{
		Version: 1, CertificateSHA256: hex.EncodeToString(pin[:]), Token: token,
		AgentPublicKey: hex.EncodeToString(cfg.PrivateKey.Public().(ed25519.PublicKey)),
		EngineID:       cfg.EngineID, Generation: cfg.Generation, EngineKind: cfg.EngineKind,
		EngineSocket: cfg.EngineSocket, DaemonID: cfg.DaemonID,
	}
	if err := json.NewEncoder(w).Encode(bundle); err != nil {
		return errors.New("cannot deliver pairing bundle")
	}
	return nil
}
