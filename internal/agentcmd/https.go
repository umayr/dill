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
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type httpsBackend uint8

const (
	httpsMonitor httpsBackend = iota + 1
	httpsStaging
	httpsPreview
	httpsApply
)

const monitorGrant = "monitor"

// The matrix is also the source of valid host-local deploy grant names. There
// is no default route, wildcard grant, or inheritance between operations.
var httpsRoutes = [...]struct {
	method, path string // a trailing slash denotes exactly one ID segment
	grant        string
	backend      httpsBackend
}{
	{http.MethodPost, "/v1/session", monitorGrant, httpsMonitor},
	{http.MethodGet, "/v1/capabilities", monitorGrant, httpsMonitor},
	{http.MethodPost, "/v1/inventory", monitorGrant, httpsMonitor},
	{http.MethodPost, "/v1/inspect", monitorGrant, httpsMonitor},
	{http.MethodPost, "/v1/logs", monitorGrant, httpsMonitor},
	{http.MethodPost, "/v1/snapshots", "stage", httpsStaging},
	{http.MethodGet, "/v1/snapshots/", "manifest", httpsStaging},
	{http.MethodPost, "/v1/deploy/preview", "preview", httpsPreview},
	{http.MethodPost, "/v1/deploy/apply", "apply", httpsApply},
	{http.MethodPost, "/v1/deploy/journal-apply", "journal-apply", httpsApply},
	{http.MethodPost, "/v1/deploy/checked-apply", "checked-apply", httpsApply},
	{http.MethodPost, "/v1/deploy/fence", "fence", httpsApply},
	{http.MethodGet, "/v1/deploy/journal", "journal", httpsApply},
	{http.MethodPost, "/v1/deploy/remove-preview", "remove-preview", httpsApply},
	{http.MethodPost, "/v1/deploy/remove", "remove", httpsApply},
	{http.MethodPost, "/v1/deploy/start-preview", "start-preview", httpsApply},
	{http.MethodPost, "/v1/deploy/start", "start", httpsApply},
	{http.MethodPost, "/v1/deploy/stop-preview", "stop-preview", httpsApply},
	{http.MethodPost, "/v1/deploy/stop", "stop", httpsApply},
	{http.MethodPost, "/v1/deploy/restart-preview", "restart-preview", httpsApply},
	{http.MethodPost, "/v1/deploy/restart", "restart", httpsApply},
	{http.MethodGet, "/v1/deploy/operations/", "status", httpsApply},
}

type httpsDeployGrants map[string]bool

func (g httpsDeployGrants) add(name string) error {
	for _, route := range httpsRoutes {
		if route.backend != httpsMonitor && name != "" && name != monitorGrant && route.grant == name {
			g[name] = true
			return nil
		}
	}
	return errors.New("unknown HTTPS deploy grant")
}

func authenticatedHTTPS(monitor, staging, preview, apply http.Handler, monitorToken, deployToken string, deployGrants httpsDeployGrants) http.Handler {
	if apply != nil {
		// Managed preview must continue through the journal-aware handler.
		preview = apply
	}
	backends := [...]http.Handler{httpsMonitor: monitor, httpsStaging: staging, httpsPreview: preview, httpsApply: apply}
	monitorHash := sha256.Sum256([]byte("Bearer " + monitorToken))
	deployHash := sha256.Sum256([]byte("Bearer " + deployToken))
	// Startup rejects duplicate credentials. Also fail closed at dispatch if
	// called without distinct credentials (including a missing monitor token).
	deployEnabled := monitorToken != "" && deployToken != "" && subtle.ConstantTimeCompare(monitorHash[:], deployHash[:]) != 1
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		headers := r.Header.Values("Authorization")
		if r.TLS == nil || len(headers) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := sha256.Sum256([]byte(headers[0]))
		isMonitor := monitorToken != "" && subtle.ConstantTimeCompare(got[:], monitorHash[:]) == 1
		isDeploy := deployEnabled && subtle.ConstantTimeCompare(got[:], deployHash[:]) == 1
		if !isMonitor && !isDeploy {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.RawPath == "" {
			for _, route := range httpsRoutes {
				if r.Method != route.method || !httpsPathMatches(route.path, r.URL.Path) {
					continue
				}
				handler := backends[route.backend]
				if handler == nil || route.backend != httpsMonitor && !deployEnabled {
					break
				}
				allowed := false
				switch {
				case route.grant == monitorGrant && route.backend == httpsMonitor:
					allowed = isMonitor
				case route.grant != "" && route.grant != monitorGrant && route.backend != httpsMonitor:
					allowed = isDeploy && deployGrants[route.grant]
				}
				if !allowed {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
				if route.path == "/v1/capabilities" {
					writeHTTPSCapabilities(w, backends[:], deployEnabled, deployGrants)
					return
				}
				handler.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func httpsPathMatches(pattern, path string) bool {
	if !strings.HasSuffix(pattern, "/") {
		return pattern == path
	}
	id, ok := strings.CutPrefix(path, pattern)
	return ok && id != "" && !strings.Contains(id, "/")
}

func readHTTPSDeployToken(path, monitorToken string, required bool) (string, error) {
	if path == "" {
		if required {
			return "", errors.New("HTTPS staging and deployment require --https-deploy-token-file")
		}
		return "", nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("HTTPS deploy token file must be an absolute path")
	}
	token, err := readBearerToken(path)
	if err != nil {
		return "", errors.New("HTTPS deploy token must be an owner-only regular file with a canonical 32-byte base64url credential")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(monitorToken)) == 1 {
		return "", errors.New("HTTPS deploy token must differ from the monitor token")
	}
	return token, nil
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

func serveHTTPS(address string, cert tls.Certificate, handler http.Handler) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer l.Close()
	server := &http.Server{
		Handler: handler, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 6 * time.Minute,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	// Bound TCP connections before TLS negotiation, including slow handshakes.
	return server.ServeTLS(newLimitedListener(l, 32), "", "")
}
