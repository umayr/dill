package metricscollect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/umayr/dill/internal/metricsmodel"
)

const goodMeminfo = "MemTotal: 100 kB\nMemFree: 10 kB\nMemAvailable:\t20 kB\nHugePages_Total: 0\n"

type hostFile struct {
	io.Reader
	closeErr error
	closed   int
}

func (f *hostFile) Close() error {
	f.closed++
	return f.closeErr
}

type shortReader struct{ io.Reader }

func (r shortReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:min(len(p), 2)]) }

func TestMeminfoParser(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		ok         bool
	}{
		{"kernel fields", goodMeminfo, true},
		{"measured zero", "MemAvailable: 0 kB\nMemTotal: 100 kB\n", true},
		{"max uint64 decodes without wrap", "MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB\n", true},
		{"missing available", "MemTotal: 100 kB\n", false},
		{"missing total", "MemAvailable: 20 kB\n", false},
		{"zero total", "MemTotal: 0 kB\nMemAvailable: 0 kB\n", false},
		{"inconsistent pair", "MemTotal: 100 kB\nMemAvailable: 101 kB\n", false},
		{"overflow", "MemTotal: 18446744073709551616 kB\nMemAvailable: 0 kB\n", false},
		{"negative", "MemTotal: 100 kB\nMemAvailable: -1 kB\n", false},
		{"wrong unit", "MemTotal: 100 KB\nMemAvailable: 20 kB\n", false},
		{"extra tokens", "MemTotal: 100 kB\nMemAvailable: 20 kB unknown\n", false},
		{"duplicate total", goodMeminfo + "MemTotal: 100 kB\n", false},
		{"duplicate available", goodMeminfo + "MemAvailable: 20 kB\n", false},
		{"truncated", strings.TrimSuffix(goodMeminfo, "\n"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := parseMeminfo([]byte(tc.text)); ok != tc.ok {
				t.Fatalf("valid = %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestHostMemoryExactLocalLimitAndShortReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		ok   bool
	}{
		{"exact", MaxMeminfoBytes, true},
		{"one over", MaxMeminfoBytes + 1, false},
		{"large", 2 * MaxMeminfoBytes, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := goodMeminfo + "Ignored:" + strings.Repeat("x", tc.size-len(goodMeminfo)-len("Ignored:\n")) + "\n"
			r := &countingReader{Reader: shortReader{Reader: strings.NewReader(data)}}
			file := &hostFile{Reader: r}
			var opened int
			c := collector(t, BoundSources{Host: newHostMemorySource(true, func(context.Context) (io.ReadCloser, error) {
				opened++
				return file, nil
			})})
			s, err := c.Collect(context.Background(), daemonID)
			if tc.ok {
				if err != nil || !s.Usable() {
					t.Fatalf("exact limit was not usable: %v", err)
				}
				checkMeasurements(t, s, true, metricsmodel.UintReading{}, metricsmodel.UintReading{})
			} else {
				unavailable(t, s, err)
			}
			if opened != 1 || file.closed != 1 || r.bytes != min(tc.size, MaxMeminfoBytes+1) {
				t.Fatalf("opens=%d closes=%d bytes=%d", opened, file.closed, r.bytes)
			}
		})
	}
}

func TestHostUnavailablePreservesIndependentAggregate(t *testing.T) {
	privateErr := errors.New("fixture failure")
	for _, tc := range []struct {
		name     string
		local    bool
		reader   io.Reader
		openErr  error
		closeErr error
	}{
		{name: "unsupported locality"},
		{name: "malformed", local: true, reader: strings.NewReader("MemTotal: 100 kB\n")},
		{name: "bytes unrepresentable", local: true, reader: strings.NewReader("MemTotal: 18446744073709551615 kB\nMemAvailable: 0 kB\n")},
		{name: "excess local parse size", local: true, reader: strings.NewReader(goodMeminfo + strings.Repeat("x", MaxMeminfoBytes))},
		{name: "open error with handle", local: true, reader: strings.NewReader(goodMeminfo), openErr: privateErr},
		{name: "read error with bytes", local: true, reader: io.MultiReader(strings.NewReader(goodMeminfo), readWithError{n: 2, err: privateErr})},
		{name: "zero progress", local: true, reader: readWithError{}},
		{name: "close error", local: true, reader: strings.NewReader(goodMeminfo), closeErr: privateErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := &hostFile{Reader: tc.reader, closeErr: tc.closeErr}
			opened := 0
			source := newHostMemorySource(tc.local, func(context.Context) (io.ReadCloser, error) {
				opened++
				return file, tc.openErr
			})
			c := collector(t, BoundSources{Host: source, Inventory: inventory(owned(1)), Memory: memory})
			checkMeasurements(t, collect(t, c), false, reading(1), reading(42))
			want := 0
			if tc.local {
				want = 1
			}
			if opened != want || file.closed != want {
				t.Fatalf("opens=%d closes=%d, want %d", opened, file.closed, want)
			}
		})
	}
}

func TestHostCancellationClosesFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &countingReader{Reader: strings.NewReader(goodMeminfo)}
	file := &hostFile{Reader: r}
	c := collector(t, BoundSources{Host: newHostMemorySource(true, func(callCtx context.Context) (io.ReadCloser, error) {
		cancel()
		<-callCtx.Done()
		return file, nil
	})})
	s, err := c.Collect(ctx, daemonID)
	unavailable(t, s, err)
	if file.closed != 1 || r.calls != 0 {
		t.Fatalf("closes=%d reads=%d", file.closed, r.calls)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestHostCancellationDuringMeteredRead(t *testing.T) {
	for _, cooperates := range []bool{true, false} {
		t.Run(fmt.Sprintf("cooperates=%t", cooperates), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				entered, release := make(chan struct{}), make(chan struct{}, 1)
				defer close(release)
				file := &hostFile{}
				var reads, opens, releases int
				c := New(func(context.Context, ReadBudget, string) (BoundSources, error) {
					return BoundSources{
						Host: newHostMemorySource(true, func(callCtx context.Context) (io.ReadCloser, error) {
							opens++
							file.Reader = readerFunc(func(p []byte) (int, error) {
								reads++
								close(entered)
								if cooperates {
									<-callCtx.Done()
									return 0, callCtx.Err()
								}
								<-release // Ignores the context; the meter cannot interrupt Read.
								return copy(p, goodMeminfo), io.EOF
							})
							return file, nil
						}),
						Release: func() { releases++ },
					}, nil
				})
				done := make(chan struct{})
				var s metricsmodel.Snapshot
				var err error
				go func() {
					defer close(done)
					s, err = c.Collect(ctx, daemonID)
				}()
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("metered read did not start")
				}
				canceledAt := time.Now()
				cancel()
				if !cooperates {
					synctest.Wait()
					select {
					case <-done:
						t.Fatal("acquisition escaped a blocked reader")
					default:
					}
					if file.closed != 0 || releases != 0 {
						t.Fatal("cleanup ran before the read returned")
					}
					release <- struct{}{}
				}
				select {
				case <-done:
				case <-time.After(CallTimeout):
					t.Fatal("canceled acquisition did not return promptly")
				}
				if cooperates && time.Since(canceledAt) != 0 {
					t.Fatal("cooperating read waited for a timer instead of cancellation")
				}
				unavailable(t, s, err)
				if opens != 1 || reads != 1 || file.closed != 1 || releases != 1 {
					t.Fatalf("opens=%d reads=%d closes=%d binding releases=%d", opens, reads, file.closed, releases)
				}
			})
		})
	}
}

func TestHostReadConsumesSharedByteBudget(t *testing.T) {
	file := &hostFile{Reader: &countingReader{Reader: strings.NewReader(goodMeminfo)}}
	c := New(func(_ context.Context, b ReadBudget, _ string) (BoundSources, error) {
		err := b.Do(func(context.Context) error {
			_, err := io.CopyN(io.Discard, b.Reader(strings.NewReader(strings.Repeat("x", MaxUpstreamBytes))), MaxUpstreamBytes-1)
			return err
		})
		return BoundSources{
			Host: newHostMemorySource(true, func(context.Context) (io.ReadCloser, error) { return file, nil }),
			Inventory: func(context.Context, ReadBudget, int) (Inventory, error) {
				t.Fatal("started inventory after global byte-budget breach")
				return Inventory{}, nil
			},
		}, err
	})
	s, err := c.Collect(context.Background(), daemonID)
	unavailable(t, s, err)
	if file.closed != 1 || file.Reader.(*countingReader).bytes != 1 {
		t.Fatalf("closes=%d bytes=%d", file.closed, file.Reader.(*countingReader).bytes)
	}
}

func TestSharedCallBudgetChargesFailedVerificationIOAndBlocksHostOverflow(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("overflow=%t", overflow), func(t *testing.T) {
			var started, opened int
			verifyCalls := MaxUpstreamCalls - 1
			if overflow {
				verifyCalls++
			}
			fixtureFailure := errors.New("failed operation still charged")
			c := New(func(_ context.Context, b ReadBudget, _ string) (BoundSources, error) {
				for range verifyCalls {
					if err := b.Do(func(context.Context) error {
						started++
						return fixtureFailure
					}); err != fixtureFailure {
						t.Fatalf("verification operation err=%v, want fixture failure", err)
					}
				}
				source := newHostMemorySource(true, func(context.Context) (io.ReadCloser, error) {
					opened++
					return io.NopCloser(strings.NewReader(goodMeminfo)), nil
				})
				return BoundSources{Host: func(ctx context.Context, b ReadBudget) (metricsmodel.HostObservation, error) {
					h, err := source(ctx, b)
					if overflow && err != ErrCallBudget {
						t.Fatalf("host operation err=%v, want ErrCallBudget", err)
					}
					return h, err
				}}, nil
			})
			s, err := c.Collect(context.Background(), daemonID)
			if overflow {
				unavailable(t, s, err)
			} else {
				if err != nil || !s.Usable() {
					t.Fatalf("host-only collection: %v", err)
				}
				checkMeasurements(t, s, true, metricsmodel.UintReading{}, metricsmodel.UintReading{})
			}
			if started != verifyCalls || started+opened != MaxUpstreamCalls {
				t.Fatalf("verification starts=%d host opens=%d, want %d total", started, opened, MaxUpstreamCalls)
			}
		})
	}
}
