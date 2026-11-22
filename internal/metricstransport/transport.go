// Package metricstransport prepares a separate, opt-in HTTPS metrics endpoint.
// It does not bind a socket or configure any other Dill endpoint.
package metricstransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	contentType       = "text/plain; version=0.0.4; charset=utf-8"
	maxResponseBytes  = 32 << 10
	minStartInterval  = 5 * time.Second
	acquisitionBudget = time.Second
	requestBudget     = 1500 * time.Millisecond
	writeBudget       = 250 * time.Millisecond
)

// Config is disabled by default. An enabled endpoint requires all singular
// fields. BindAddress must be a literal private or loopback IP and nonzero port;
// ServerName is the DNS name or IP that the scrape client will verify.
// Credential paths must be clean, absolute paths to distinct owner-only files
// on trusted local storage. The token file contains dill-scrape-v1. followed
// by 32 random bytes encoded as unpadded base64url, optionally ending in one
// newline. This is not a Brine credential. Restart with newly provisioned
// files to rotate; there is no grace token or implicit expiry.
type Config struct {
	Enabled     bool
	BindAddress string
	ServerName  string
	CAFile      string
	CertFile    string
	KeyFile     string
	TokenFile   string

	// ExpectedDaemonID is enrollment input, not proof of an observed identity.
	ExpectedDaemonID string

	// The caller supplies existing binds and credential paths for collision
	// checks. Paths must exist; only metadata is inspected, not file contents.
	ReservedAddresses       []string
	ReservedCredentialFiles []string
}

// Hooks are trusted in-process dependencies. AcceptsText is the renderer's
// pure negotiation function and sees only Accept headers. AcquireRender must
// verify the expected daemon identity on every call, obey the acquisition
// context and the contract's upstream limits (call times, concurrency, counts,
// and bytes), and return fresh bounded Prometheus text only for usable
// observations. It transfers ownership of the returned bytes; errors are not
// sent to clients. Hooks must not launch work that outlives the call.
type Hooks struct {
	AcceptsText   func(http.Header) bool
	AcquireRender func(ctx context.Context, expectedDaemonID string) ([]byte, error)
}

// Prepare validates configuration and loads credentials without opening a
// listener. A disabled configuration returns nil, nil without inspecting files
// or hooks. Use one prepared server per agent, only with its own exclusive TLS
// listener; do not mount Handler on a shared server or fall back to plaintext.
func Prepare(cfg Config, hooks Hooks) (*http.Server, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if hooks.AcceptsText == nil || hooks.AcquireRender == nil {
		return nil, errors.New("metrics requires negotiation and acquisition hooks")
	}
	address, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	certificate, bearerHash, err := loadCredentials(cfg)
	if err != nil {
		return nil, err
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	return &http.Server{
		Addr:    address,
		Handler: newHandler(bearerHash, cfg.ExpectedDaemonID, hooks, time.Now),
		TLSConfig: &tls.Config{
			MinVersion:             tls.VersionTLS13,
			Certificates:           []tls.Certificate{certificate},
			SessionTicketsDisabled: true,
		},
		Protocols:         protocols,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}, nil
}

type admission struct {
	mu     sync.Mutex
	active bool
	last   time.Time
}

func (a *admission) start(now func() time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := now()
	if a.active || (!a.last.IsZero() && t.Sub(a.last) < minStartInterval) {
		return false
	}
	a.active, a.last = true, t
	return true
}

func (a *admission) finish() {
	a.mu.Lock()
	a.active = false
	a.mu.Unlock()
}

func newHandler(bearerHash [32]byte, daemonID string, hooks Hooks, cadenceClock func() time.Time) http.Handler {
	var gate admission
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestBudget)
		defer cancel()
		absolute, _ := ctx.Deadline()
		w.Header().Set("Cache-Control", "no-store")
		unexpectedBody := r.ContentLength != 0 || len(r.TransferEncoding) != 0
		if unexpectedBody {
			// Do not let HTTP/1 drain an unexpected body before the response.
			w.Header().Set("Connection", "close")
		}

		respond := func(status int, body []byte) {
			deadline := time.Now().Add(writeBudget)
			if absolute.Before(deadline) {
				deadline = absolute
			}
			// Recorders don't implement write deadlines. A real HTTP/1 writer
			// does; the dedicated server also has an outer write timeout.
			err := http.NewResponseController(w).SetWriteDeadline(deadline)
			if (err != nil && !errors.Is(err, http.ErrNotSupported)) ||
				(status == http.StatusOK && (ctx.Err() != nil || !time.Now().Before(absolute))) {
				status = http.StatusServiceUnavailable
			}
			if status != http.StatusOK {
				http.Error(w, http.StatusText(status), status)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}

		if r.Method != http.MethodGet || r.URL.Path != "/metrics" || r.URL.RawPath != "" ||
			r.URL.ForceQuery || r.URL.RawQuery != "" || r.URL.Opaque != "" || r.URL.Fragment != "" ||
			(r.RequestURI != "" && r.RequestURI != "/metrics") {
			respond(http.StatusNotFound, nil)
			return
		}
		if r.TLS == nil || !authorized(r.Header.Values("Authorization"), bearerHash) {
			respond(http.StatusUnauthorized, nil)
			return
		}
		if unexpectedBody {
			respond(http.StatusBadRequest, nil)
			return
		}
		accept := make(http.Header, 1)
		if values := r.Header.Values("Accept"); len(values) != 0 {
			accept["Accept"] = slices.Clone(values)
		}
		if !hooks.AcceptsText(accept) {
			respond(http.StatusNotAcceptable, nil)
			return
		}
		if ctx.Err() != nil || !time.Now().Before(absolute) {
			respond(http.StatusServiceUnavailable, nil)
			return
		}
		if !gate.start(cadenceClock) {
			respond(http.StatusTooManyRequests, nil)
			return
		}
		defer gate.finish()

		acquireCtx, endAcquire := context.WithTimeout(ctx, acquisitionBudget)
		defer endAcquire()
		if acquireCtx.Err() != nil {
			respond(http.StatusServiceUnavailable, nil)
			return
		}
		body, err := hooks.AcquireRender(acquireCtx, daemonID)
		if err != nil || len(body) == 0 || len(body) > maxResponseBytes || acquireCtx.Err() != nil {
			respond(http.StatusServiceUnavailable, nil)
			return
		}
		body = bytes.Clone(body)
		// Check wall/monotonic expiration directly as well as cancellation:
		// a context timer may not yet have been scheduled at its deadline.
		acquireDeadline, _ := acquireCtx.Deadline()
		if acquireCtx.Err() != nil || !time.Now().Before(acquireDeadline) {
			respond(http.StatusServiceUnavailable, nil)
			return
		}
		respond(http.StatusOK, body)
	})
}

func authorized(values []string, want [32]byte) bool {
	if len(values) != 1 {
		return false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || len(token) != tokenLength {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
