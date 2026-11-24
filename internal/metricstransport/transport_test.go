package metricstransport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticToken(b byte) string {
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32))
}

const testMetrics = "# HELP dill_test Synthetic test only.\n# TYPE dill_test gauge\ndill_test 1\n"

func scrapeRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.TLS = httptest.NewRequest(http.MethodGet, "https://scrape.invalid/metrics", nil).TLS
	r.Header.Set("Authorization", "Bearer "+syntheticToken(1))
	return r
}

func record(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func testHandler(acquire func(context.Context, string) ([]byte, error)) http.Handler {
	return newHandler(sha256.Sum256([]byte(syntheticToken(1))), "test-daemon-id", Hooks{
		AcceptsText:   func(h http.Header) bool { return h.Get("Accept") != "application/openmetrics-text" },
		AcquireRender: acquire,
	}, time.Now)
}

func TestRouteAuthNegotiationBeforeCollection(t *testing.T) {
	calls, negotiated := 0, 0
	h := newHandler(sha256.Sum256([]byte(syntheticToken(1))), "test-daemon-id", Hooks{
		AcceptsText: func(h http.Header) bool {
			negotiated++
			if h.Get("Authorization") != "" || h.Get("Accept-Encoding") != "" {
				t.Fatal("renderer negotiation got extra headers")
			}
			return h.Get("Accept") != "application/openmetrics-text"
		},
		AcquireRender: func(_ context.Context, id string) ([]byte, error) {
			calls++
			if id != "test-daemon-id" {
				t.Fatal("wrong expected daemon ID")
			}
			return []byte(testMetrics), nil
		},
	}, time.Now)
	for _, tt := range []struct {
		name   string
		change func(*http.Request)
		status int
	}{
		{"other route", func(r *http.Request) { r.URL.Path = "/v1/session" }, 404},
		{"head", func(r *http.Request) { r.Method = http.MethodHead }, 404},
		{"slash", func(r *http.Request) { r.URL.Path = "/metrics/" }, 404},
		{"encoded", func(r *http.Request) { r.URL.RawPath = "/%6detrics" }, 404},
		{"absolute form", func(r *http.Request) { r.RequestURI = "https://scrape.invalid/metrics" }, 404},
		{"bare query", func(r *http.Request) { r.URL.ForceQuery = true }, 404},
		{"query", func(r *http.Request) { r.URL.RawQuery = "ignored=x" }, 404},
		{"plaintext", func(r *http.Request) { r.TLS = nil }, 401},
		{"missing", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"wrong before Accept", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+syntheticToken(2))
			r.Header.Set("Accept", "application/openmetrics-text")
		}, 401},
		{"other credential format", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(syntheticToken(1), tokenPrefix))
		}, 401},
		{"duplicate header", func(r *http.Request) { r.Header.Add("Authorization", r.Header.Get("Authorization")) }, 401},
		{"joined header", func(r *http.Request) {
			h := r.Header.Get("Authorization")
			r.Header.Set("Authorization", h+", "+h)
		}, 401},
		{"body length", func(r *http.Request) { r.ContentLength = 1 }, 400},
		{"chunked body", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }, 400},
		{"negotiation", func(r *http.Request) { r.Header.Set("Accept", "application/openmetrics-text") }, 406},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := scrapeRequest()
			tt.change(r)
			w := record(h, r)
			if w.Code != tt.status || w.Body.String() != http.StatusText(tt.status)+"\n" || calls != 0 {
				t.Fatalf("status=%d, collections=%d, generic body=%v", w.Code, calls, w.Body.String() == http.StatusText(tt.status)+"\n")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" ||
				w.Header().Get("WWW-Authenticate") != "" || w.Header().Get("Retry-After") != "" {
				t.Fatal("unexpected refusal policy")
			}
			if tt.status == 400 && w.Header().Get("Connection") != "close" {
				t.Fatal("unexpected request bodies must not be drained")
			}
		})
	}
	if negotiated != 1 {
		t.Fatalf("negotiation ran on %d requests; want only the authenticated 406", negotiated)
	}
	r := scrapeRequest()
	r.Header.Set("Authorization", "bearer "+syntheticToken(1))
	r.Header.Set("Accept-Encoding", "gzip")
	w := record(h, r)
	if w.Code != 200 || w.Body.String() != testMetrics || calls != 1 ||
		w.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" ||
		w.Header().Get("Content-Encoding") != "" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected first authorized result: status %d, calls %d", w.Code, calls)
	}
}

func TestConcurrencyAndCadenceChargeFailedAttempts(t *testing.T) {
	base := time.Now()
	var elapsed atomic.Int64
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	h := newHandler(sha256.Sum256([]byte(syntheticToken(1))), "id", Hooks{
		AcceptsText: func(http.Header) bool { return true },
		AcquireRender: func(ctx context.Context, _ string) ([]byte, error) {
			if calls.Add(1) != 1 {
				return []byte(testMetrics), nil
			}
			close(started)
			select {
			case <-release:
				return nil, context.Canceled
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}, func() time.Time { return base.Add(time.Duration(elapsed.Load())) })
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- record(h, scrapeRequest()) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not begin")
	}
	// Move beyond the cadence interval while the first request is still active.
	elapsed.Store(int64(minStartInterval + time.Second))
	concurrent := record(h, scrapeRequest()).Code
	close(release)
	if w := <-first; w.Code != 503 || concurrent != 429 {
		t.Fatalf("first status=%d, concurrent status=%d", w.Code, concurrent)
	}
	elapsed.Store(int64(minStartInterval - time.Nanosecond))
	if w := record(h, scrapeRequest()); w.Code != 429 || w.Header().Get("Retry-After") != "" || calls.Load() != 1 {
		t.Fatalf("cadence refusal status=%d, acquisition count=%d", w.Code, calls.Load())
	}
	elapsed.Store(int64(minStartInterval))
	if w := record(h, scrapeRequest()); w.Code != 200 || calls.Load() != 2 {
		t.Fatalf("cadence boundary status=%d, acquisition count=%d", w.Code, calls.Load())
	}
}

func TestUnusableResponseDoesNotCommitMetrics(t *testing.T) {
	for _, tt := range []struct {
		name string
		body []byte
		err  error
	}{
		{"no observation", nil, nil},
		{"oversized response", bytes.Repeat([]byte{'x'}, maxResponseBytes+1), nil},
		{"acquisition error with partial output", []byte(testMetrics), errors.New("sensitive internal acquisition error")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := testHandler(func(context.Context, string) ([]byte, error) { return tt.body, tt.err })
			w := record(h, scrapeRequest())
			if w.Code != 503 || w.Body.String() != "Service Unavailable\n" || w.Header().Get("Content-Type") == contentType {
				t.Fatalf("failed response was not generic: status %d", w.Code)
			}
		})
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
	err      error
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return w.err
}

func TestDeadlinesAndExactResponseLimit(t *testing.T) {
	start := time.Now()
	h := testHandler(func(ctx context.Context, _ string) ([]byte, error) {
		d, ok := ctx.Deadline()
		if !ok || d.After(time.Now().Add(acquisitionBudget)) || d.Before(start.Add(acquisitionBudget)) {
			t.Fatal("wrong acquisition budget")
		}
		return bytes.Repeat([]byte{'x'}, maxResponseBytes), nil
	})
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, scrapeRequest())
	if w.Code != 200 || w.Body.Len() != maxResponseBytes ||
		w.deadline.Before(start.Add(writeBudget)) || w.deadline.After(time.Now().Add(writeBudget)) {
		t.Fatalf("status %d, response size %d, write deadline %v", w.Code, w.Body.Len(), w.deadline)
	}

	// A shorter upstream request deadline automatically narrows both budgets.
	parent, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	d, _ := parent.Deadline()
	h = testHandler(func(ctx context.Context, _ string) ([]byte, error) {
		if got, _ := ctx.Deadline(); got != d {
			t.Fatal("acquisition failed to inherit earlier deadline")
		}
		<-ctx.Done()
		return []byte(testMetrics), nil // even a nominally successful late result is discarded
	})
	w = &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(w, scrapeRequest().WithContext(parent))
	if w.Code != 503 || w.deadline != d || w.Body.String() != "Service Unavailable\n" {
		t.Fatal("late result committed or write deadline extended")
	}
	if w := record(h, scrapeRequest()); w.Code != 429 {
		t.Fatalf("expired acquisition did not consume cadence: %d", w.Code)
	}

	calls := 0
	h = testHandler(func(context.Context, string) ([]byte, error) {
		calls++
		return []byte(testMetrics), nil
	})
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if w := record(h, scrapeRequest().WithContext(canceled)); w.Code != 503 || calls != 0 {
		t.Fatalf("already-canceled request status: %d, calls: %d", w.Code, calls)
	}
	w = &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), err: errors.New("cannot set write deadline")}
	h.ServeHTTP(w, scrapeRequest())
	if w.Code != 503 || w.Body.String() != "Service Unavailable\n" {
		t.Fatal("metrics committed without the supported write deadline")
	}
}
