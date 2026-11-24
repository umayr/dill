package metricstransport

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
)

const tokenPrefix = "dill-scrape-v1."
const tokenLength = len(tokenPrefix) + 43 // unpadded base64url encoding of 32 bytes

func loadCredentials(cfg Config) (tls.Certificate, [32]byte, error) {
	var empty tls.Certificate
	var emptyHash [32]byte
	var reserved []os.FileInfo
	for _, path := range cfg.ReservedCredentialFiles {
		// Follow aliases for collision detection only. Never open these files.
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return empty, emptyHash, errors.New("cannot verify reserved metrics credential metadata")
		}
		reserved = append(reserved, info)
	}

	caPEM, err := readCredential(cfg.CAFile, 64<<10, reserved)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics CA file")
	}
	chainPEM, err := readCredential(cfg.CertFile, 64<<10, reserved)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics certificate file")
	}
	keyPEM, err := readCredential(cfg.KeyFile, 16<<10, reserved)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics private key file")
	}
	defer clear(keyPEM)
	caCerts, err := parseCertificates(caPEM)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics CA file")
	}
	chain, err := parseCertificates(chainPEM)
	if err != nil || chain[0].IsCA || !validTLSLeaf(chain[0]) {
		return empty, emptyHash, errors.New("invalid metrics server certificate chain")
	}
	roots := x509.NewCertPool()
	for _, ca := range caCerts {
		if !ca.IsCA || !ca.BasicConstraintsValid {
			return empty, emptyHash, errors.New("metrics trust bundle must contain CA certificates only")
		}
		roots.AddCert(ca)
	}
	intermediates := x509.NewCertPool()
	for i, ca := range chain[1:] {
		if !ca.IsCA || !ca.BasicConstraintsValid || !bytes.Equal(chain[i].RawIssuer, ca.RawSubject) ||
			chain[i].CheckSignatureFrom(ca) != nil {
			return empty, emptyHash, errors.New("invalid metrics server certificate chain")
		}
		intermediates.AddCert(ca)
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{
		DNSName: cfg.ServerName, Roots: roots, Intermediates: intermediates,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return empty, emptyHash, errors.New("metrics server certificate fails SAN, validity, or CA verification")
	}
	if !singlePrivateKey(keyPEM) {
		return empty, emptyHash, errors.New("invalid metrics TLS private key file")
	}
	cert, err := tls.X509KeyPair(chainPEM, keyPEM)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics TLS key pair")
	}
	cert.Leaf = chain[0]

	raw, err := readCredential(cfg.TokenFile, int64(tokenLength+1), reserved)
	if err != nil {
		return empty, emptyHash, errors.New("invalid metrics token file")
	}
	defer clear(raw)
	token := bytes.TrimSuffix(raw, []byte("\n"))
	var decoded [32]byte
	defer clear(decoded[:])
	if len(token) != tokenLength || !bytes.HasPrefix(token, []byte(tokenPrefix)) {
		return empty, emptyHash, errors.New("invalid metrics scrape token format")
	}
	n, err := base64.RawURLEncoding.Strict().Decode(decoded[:], token[len(tokenPrefix):])
	if err != nil || n != len(decoded) {
		return empty, emptyHash, errors.New("invalid metrics scrape token format")
	}
	return cert, sha256.Sum256(token), nil
}

func validTLSLeaf(cert *x509.Certificate) bool {
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false // TLS 1.3 uses the certificate to sign, not encrypt.
	}
	switch key := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return key.N.BitLen() >= 2048
	case *ecdsa.PublicKey:
		bits := key.Params().BitSize
		return bits == 256 || bits == 384 || bits == 521
	case ed25519.PublicKey:
		return true
	default:
		return false
	}
}

func singlePrivateKey(contents []byte) bool {
	contents = bytes.TrimSpace(contents)
	for _, kind := range []string{"PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY"} {
		begin := []byte("-----BEGIN " + kind + "-----")
		if !bytes.HasPrefix(contents, begin) || bytes.Count(contents, []byte("-----BEGIN ")) != 1 {
			continue
		}
		block, rest := pem.Decode(contents)
		if block == nil {
			return false
		}
		defer clear(block.Bytes)
		return block.Type == kind && len(block.Headers) == 0 && len(bytes.TrimSpace(rest)) == 0
	}
	return false
}

func parseCertificates(contents []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	invalid := errors.New("expected certificate-only PEM")
	for remaining := bytes.TrimSpace(contents); len(remaining) != 0; remaining = bytes.TrimSpace(remaining) {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----\n")) &&
			!bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----\r\n")) {
			return nil, invalid
		}
		// Decode one isolated block, rather than letting pem.Decode silently
		// skip malformed PEM or non-certificate material between blocks.
		const end = "-----END CERTIFICATE-----"
		idx := bytes.Index(remaining, []byte(end))
		if idx < 0 {
			return nil, invalid
		}
		idx += len(end)
		block, rest := pem.Decode(remaining[:idx])
		if block == nil || len(rest) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, invalid
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, invalid
		}
		certs = append(certs, cert)
		remaining = remaining[idx:]
	}
	if len(certs) == 0 {
		return nil, invalid
	}
	return certs, nil
}
