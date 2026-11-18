package agentcmd

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"time"
)

// The HTTP allowlist never delegates reveal. Host opt-ins progressively add
// staging, preview, and checked creation for the authenticated operator.
func authenticatedHTTPS(next, staging, preview, apply http.Handler, token string) http.Handler {
	want := sha256.Sum256([]byte("Bearer " + token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		headers := r.Header.Values("Authorization")
		if r.TLS == nil || len(headers) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := sha256.Sum256([]byte(headers[0]))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/session", "/v1/inventory", "/v1/inspect", "/v1/logs":
			next.ServeHTTP(w, r)
		case "/v1/deploy/preview":
			if preview != nil && r.Method == http.MethodPost && r.URL.RawPath == "" {
				if apply != nil {
					w.Header().Set("X-Dill-Create-Enabled", "1")
				}
				preview.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		default:
			if apply != nil && r.URL.RawPath == "" && (r.Method == http.MethodPost && r.URL.Path == "/v1/deploy/apply" ||
				r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/deploy/operations/")) {
				apply.ServeHTTP(w, r)
				return
			}
			if staging != nil && r.URL.RawPath == "" && (r.Method == http.MethodPost && r.URL.Path == "/v1/snapshots" ||
				r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/snapshots/")) {
				staging.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		}
	})
}

func readBearerToken(path string) (string, error) {
	denied := errors.New("HTTPS token must be an owner-only regular file with a canonical 32-byte base64url credential")
	raw, err := readPrivateHTTPSFile(path, 44)
	token := strings.TrimSuffix(string(raw), "\n")
	decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || decodeErr != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return "", denied
	}
	return token, nil
}

func readPrivateHTTPSFile(path string, limit int64) ([]byte, error) {
	denied := errors.New("HTTPS credential must be a bounded owner-only regular file")
	// Open nonblocking so a misconfigured FIFO cannot stall startup. Do not
	// accept a symlink for the final path component.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, denied
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, denied
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || info.Size() > limit {
		return nil, denied
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, denied
	}
	return raw, nil
}

func httpsCredentials(address, certFile, keyFile, tokenFile string) (tls.Certificate, string, error) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil || addr.Port() == 0 || addr.Addr().Zone() != "" || !(addr.Addr().Unmap().IsPrivate() || addr.Addr().Unmap().IsLoopback()) {
		return tls.Certificate{}, "", errors.New("HTTPS listener requires an explicit private or loopback IP and port")
	}
	token, err := readBearerToken(tokenFile)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	privateKey, err := readPrivateHTTPSFile(keyFile, 16<<10)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	certPEM, err := readPrivateHTTPSFile(certFile, 64<<10)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	cert, err := tls.X509KeyPair(certPEM, privateKey)
	if err != nil {
		return tls.Certificate{}, "", errors.New("cannot load HTTPS certificate and key")
	}
	return cert, token, nil
}

func serveHTTPS(address, certFile, keyFile, tokenFile string, handler, staging, preview, apply http.Handler) error {
	cert, token, err := httpsCredentials(address, certFile, keyFile, tokenFile)
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer l.Close()
	server := &http.Server{
		Handler: authenticatedHTTPS(handler, staging, preview, apply, token), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	// Bound TCP connections before TLS negotiation, including slow handshakes.
	return server.ServeTLS(newLimitedListener(l, 32), "", "")
}
