package protocol

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type memoryTransport struct {
	input         []byte
	readCode      Code
	out           bytes.Buffer
	diagnostics   []Code
	reads, writes int
	unsupported   bool
	writeError    bool
	afterWrite    func(int)
	beforeFinal   func()
	partialAt     int
	deadlines     []time.Time
}

func (m *memoryTransport) Supported(bool) bool { return !m.unsupported }
func (m *memoryTransport) ReadRequest(context.Context) ([]byte, Code) {
	m.reads++
	return m.input, m.readCode
}
func (m *memoryTransport) Write(ctx context.Context, b []byte) error {
	d, _ := ctx.Deadline()
	m.deadlines = append(m.deadlines, d)
	m.writes++
	if m.writes == m.partialAt {
		m.out.Write(b[:len(b)/2])
		return errors.New("SENTINEL")
	}
	if m.writeError {
		return errors.New("SENTINEL")
	}
	m.out.Write(b)
	if m.afterWrite != nil {
		m.afterWrite(m.writes)
	}
	return nil
}
func (m *memoryTransport) WriteFinal(ctx context.Context, choose func() ([]byte, error)) error {
	if m.beforeFinal != nil {
		m.beforeFinal()
	}
	b, err := choose()
	if err != nil {
		return err
	}
	return m.Write(ctx, b)
}
func (m *memoryTransport) Diagnostic(ctx context.Context, c Code) {
	d, _ := ctx.Deadline()
	m.deadlines = append(m.deadlines, d)
	m.diagnostics = append(m.diagnostics, c)
}
func fixedID(context.Context) (string, Code) { return fixtureID, "" }
func serveTest(ctx context.Context, mode Mode, m *memoryTransport, id func(context.Context) (string, Code)) int {
	return serve(ctx, mode, m, id, time.Second, time.Second)
}

func TestServeSuccessAndDiscovery(t *testing.T) {
	for _, mode := range []Mode{DiscoveryMode, CheckMode} {
		m := &memoryTransport{input: []byte(goodRequest)}
		if got := serveTest(context.Background(), mode, m, fixedID); got != 0 {
			t.Fatal(got)
		}
		records, err := DecodeStream(m.out.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		wantRecords, wantReads := 1, 0
		if mode == CheckMode {
			wantRecords, wantReads = 2, 1
		}
		if len(records) != wantRecords || m.reads != wantReads || len(m.diagnostics) != 0 {
			t.Fatalf("records=%d reads=%d diagnostics=%v", len(records), m.reads, m.diagnostics)
		}
	}
}
func TestServeSafeFailures(t *testing.T) {
	for _, tt := range []struct {
		name  string
		mode  Mode
		input string
		read  Code
		id    func(context.Context) (string, Code)
		want  Code
	}{
		{"invalid args", InvalidMode, "", "", fixedID, InvalidArguments},
		{"input", CheckMode, `{"SENTINEL":"SENTINEL"}`, "", fixedID, InvalidRequest},
		{"read", CheckMode, "", InternalError, fixedID, InternalError},
		{"version", CheckMode, strings.Replace(goodRequest, Version, "SENTINEL", 1), "", fixedID, UnsupportedProtocol},
		{"entropy", CheckMode, goodRequest, "", func(context.Context) (string, Code) { return "SENTINEL", InternalError }, InternalError},
		{"bad ID", CheckMode, goodRequest, "", func(context.Context) (string, Code) { return "SENTINEL", "" }, InternalError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := &memoryTransport{input: []byte(tt.input), readCode: tt.read}
			if got := serveTest(context.Background(), tt.mode, m, tt.id); got != tt.want.ExitCode() {
				t.Fatal(got)
			}
			rec, err := DecodeStream(m.out.Bytes())
			if err != nil || len(rec) != 1 {
				t.Fatalf("%v %s", err, m.out.String())
			}
			result := rec[0].(BootstrapResult)
			if result.Errors[0].Code != tt.want {
				t.Fatal(result)
			}
			if strings.Contains(m.out.String(), "SENTINEL") || len(m.diagnostics) != 0 {
				t.Fatal("unsafe failure")
			}
		})
	}
}
func TestServeCancellationAndDelivery(t *testing.T) {
	t.Run("preaccept", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		m := &memoryTransport{}
		if serveTest(ctx, CheckMode, m, fixedID) != 1 {
			t.Fatal("exit")
		}
		if !bytes.Contains(m.out.Bytes(), []byte(`"CANCELLED"`)) || m.reads != 0 {
			t.Fatal(m.out.String())
		}
	})
	t.Run("after accepted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := &memoryTransport{input: []byte(goodRequest), afterWrite: func(n int) {
			if n == 1 {
				cancel()
			}
		}}
		if serveTest(ctx, CheckMode, m, fixedID) != 1 {
			t.Fatal("exit")
		}
		r, err := DecodeStream(m.out.Bytes())
		if err != nil || r[1].(Result).Outcome != "cancelled" {
			t.Fatal(err, m.out.String())
		}
	})
	t.Run("cancel while waiting for final", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := &memoryTransport{input: []byte(goodRequest), beforeFinal: cancel}
		if serveTest(ctx, CheckMode, m, fixedID) != 1 {
			t.Fatal("ignored cancellation before first final byte")
		}
		r, err := DecodeStream(m.out.Bytes())
		if err != nil || r[1].(Result).Outcome != "cancelled" {
			t.Fatal(err, m.out.String())
		}
	})
	t.Run("independent output budget", func(t *testing.T) {
		m := &memoryTransport{input: []byte(goodRequest), afterWrite: func(n int) {
			if n == 1 {
				time.Sleep(40 * time.Millisecond)
			}
		}}
		if serve(context.Background(), CheckMode, m, fixedID, 20*time.Millisecond, time.Second) != 0 {
			t.Fatal("completed acquisition deadline leaked into output phase")
		}
	})
	t.Run("late cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		m := &memoryTransport{input: []byte(goodRequest), afterWrite: func(n int) {
			if n == 2 {
				cancel()
			}
		}}
		if serveTest(ctx, CheckMode, m, fixedID) != 0 {
			t.Fatal("rewrote success")
		}
	})
	t.Run("broken", func(t *testing.T) {
		m := &memoryTransport{input: []byte(goodRequest), writeError: true}
		if serveTest(context.Background(), CheckMode, m, fixedID) != 1 || m.writes != 1 || len(m.diagnostics) != 1 || m.diagnostics[0] != OutputFailure {
			t.Fatal(m)
		}
	})
	t.Run("partial final never replaced", func(t *testing.T) {
		m := &memoryTransport{input: []byte(goodRequest), partialAt: 2}
		if serveTest(context.Background(), CheckMode, m, fixedID) != 1 || m.writes != 2 || len(m.diagnostics) != 1 || m.diagnostics[0] != OutputFailure {
			t.Fatal(m)
		}
		if len(m.deadlines) != 3 || m.deadlines[0].IsZero() || !m.deadlines[0].Equal(m.deadlines[1]) || !m.deadlines[0].Equal(m.deadlines[2]) {
			t.Fatal("output deadline renewed", m.deadlines)
		}
		if bytes.Count(m.out.Bytes(), []byte{'\n'}) != 1 || bytes.Contains(m.out.Bytes(), []byte("SENTINEL")) {
			t.Fatal(m.out.String())
		}
		if _, err := DecodeStream(m.out.Bytes()); err == nil {
			t.Fatal("partial stream accepted")
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		m := &memoryTransport{unsupported: true}
		if serveTest(context.Background(), CheckMode, m, fixedID) != 1 || m.writes != 0 || m.reads != 0 || m.diagnostics[0] != UnsupportedTransport {
			t.Fatal(m)
		}
	})
}
func TestCodesClosed(t *testing.T) {
	for c, msg := range messages {
		if c.Message() != msg || strings.Contains(msg, "SENTINEL") {
			t.Fatal(c)
		}
	}
	if Code("SENTINEL").Message() != InternalError.Message() {
		t.Fatal("echo")
	}
}
