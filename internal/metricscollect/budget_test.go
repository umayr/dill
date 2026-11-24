package metricscollect

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/metricsmodel"
)

type countingReader struct {
	io.Reader
	bytes, calls int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.bytes += n
	r.calls++
	return n, err
}

func TestByteBudgetSharedBeforeDecodeAndReset(t *testing.T) {
	const half = MaxUpstreamBytes / 2
	const hostRead = 24
	const failureRead = 30
	const remaining = MaxUpstreamBytes - half - hostRead - failureRead
	var verifyReads, hostReads, failureReads, sampleReads, sampleCalls int
	var failRead = errors.New("private read failure")
	var attempt int
	c := New(func(ctx context.Context, budget ReadBudget, _ string) (BoundSources, error) {
		attempt++
		if attempt == 2 {
			// A new acquisition starts with the full allowance.
			_, err := io.CopyN(io.Discard, budget.Reader(strings.NewReader(strings.Repeat("x", MaxUpstreamBytes))), MaxUpstreamBytes)
			return BoundSources{Host: host}, err
		}
		verify := &countingReader{Reader: strings.NewReader(strings.Repeat("x", half))}
		_, err := io.Copy(io.Discard, budget.Reader(verify))
		verifyReads = verify.bytes
		if err != nil {
			return BoundSources{}, err
		}
		return BoundSources{
			Host: func(ctx context.Context, budget ReadBudget) (metricsmodel.HostObservation, error) {
				r := &countingReader{Reader: strings.NewReader(strings.Repeat("x", hostRead))}
				_, err := io.Copy(io.Discard, budget.Reader(r))
				hostReads = r.bytes
				if err != nil {
					return metricsmodel.HostObservation{}, err
				}
				return host(ctx, budget)
			},
			Inventory: func(ctx context.Context, budget ReadBudget, limit int) (Inventory, error) {
				r := &countingReader{Reader: readWithError{n: failureRead, err: failRead}}
				_, err := io.Copy(io.Discard, budget.Reader(r))
				failureReads = r.bytes
				if err != failRead {
					t.Fatalf("expected fixture read error, got %v", err)
				}
				// The budget accounts for partial error reads even when a
				// source obtains its evidence independently of those bytes.
				return inventory(owned(1), owned(2))(ctx, budget, limit)
			},
			Memory: func(ctx context.Context, budget ReadBudget, id metricsmodel.ContainerIdentity) (metricsmodel.ContainerMemoryObservation, error) {
				sampleCalls++
				r := &countingReader{Reader: strings.NewReader(`"` + strings.Repeat("x", remaining) + `"`)}
				var value string
				if err := json.NewDecoder(budget.Reader(r)).Decode(&value); !errors.Is(err, ErrReadBudget) {
					t.Fatalf("decoder error = %v, want ErrReadBudget", err)
				}
				sampleReads = r.bytes
				return memory(ctx, budget, id) // Swallowing the error must not produce success.
			},
		}, nil
	})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
	if verifyReads != half || hostReads != hostRead || failureReads != failureRead || sampleReads != remaining || sampleCalls != 1 {
		t.Fatalf("read counts were verifier=%d host=%d inventory=%d sample=%d calls=%d", verifyReads, hostReads, failureReads, sampleReads, sampleCalls)
	}
	checkMeasurements(t, collect(t, c), true, metricsmodel.UintReading{}, metricsmodel.UintReading{})
}

type readWithError struct {
	n   int
	err error
}

func (r readWithError) Read(p []byte) (int, error) { return min(len(p), r.n), r.err }

func TestCanceledReaderDoesNotTouchUpstream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := collector(t, BoundSources{Host: func(callCtx context.Context, budget ReadBudget) (metricsmodel.HostObservation, error) {
		r := &countingReader{Reader: strings.NewReader("private bytes")}
		cancel()
		<-callCtx.Done()
		if n, err := budget.Reader(r).Read(make([]byte, 10)); n != 0 || err != context.Canceled || r.calls != 0 {
			t.Fatalf("canceled read: n=%d err=%v upstream calls=%d", n, err, r.calls)
		}
		return host(callCtx, budget)
	}})
	defer cancel()
	s, err := c.Collect(ctx, daemonID)
	unavailable(t, s, err)
}

func TestByteLimitDoesNotProbeUpstreamForEOF(t *testing.T) {
	var r countingReader
	c := collector(t, BoundSources{Host: func(ctx context.Context, budget ReadBudget) (metricsmodel.HostObservation, error) {
		r.Reader = strings.NewReader(strings.Repeat("x", MaxUpstreamBytes))
		metered := budget.Reader(&r)
		if _, err := io.CopyN(io.Discard, metered, MaxUpstreamBytes); err != nil {
			t.Fatal(err)
		}
		calls := r.calls
		if n, err := metered.Read(make([]byte, 1)); n != 0 || err != ErrReadBudget || r.calls != calls || r.bytes != MaxUpstreamBytes {
			t.Fatalf("read at limit: n=%d err=%v bytes=%d extra calls=%d", n, err, r.bytes, r.calls-calls)
		}
		return host(ctx, budget)
	}})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
}
