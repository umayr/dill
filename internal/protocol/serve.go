package protocol

import (
	"context"
	"time"
)

type Mode int

const (
	DiscoveryMode Mode = iota
	CheckMode
	InvalidMode
)
const (
	AcquisitionBudget = 5 * time.Second
	OutputBudget      = time.Second
)

// Serve handles exactly one metadata-only invocation. No request data is ever
// copied to output. An unsupported output cannot receive a fabricated result.
func Serve(ctx context.Context, mode Mode, t Transport) int {
	return serve(ctx, mode, t, newID, AcquisitionBudget, OutputBudget)
}
func serve(ctx context.Context, mode Mode, t Transport, id func(context.Context) (string, Code), acquisition, output time.Duration) int {
	start := time.Now()
	acquire, cancel := context.WithDeadline(ctx, start.Add(acquisition))
	defer cancel()
	var code Code
	var operationID string
	supported := t.Supported(mode == CheckMode)
	switch {
	case !supported:
		code = UnsupportedTransport
	case mode != DiscoveryMode && mode != CheckMode:
		code = InvalidArguments
	case acquire.Err() != nil:
		code = contextCode(acquire)
	case mode == CheckMode:
		var b []byte
		b, code = t.ReadRequest(acquire)
		if code == "" {
			_, code = Parse(b)
		}
		if acquire.Err() != nil {
			code = contextCode(acquire)
		}
		if code == "" {
			operationID, code = id(acquire)
		}
		if acquire.Err() != nil {
			code = contextCode(acquire)
		}
		if code == "" && !validHeader(Version, operationID, Operation, 1) {
			code = InternalError
		}
	}
	if code == "" && !time.Now().Before(start.Add(acquisition)) {
		code = DeadlineExceeded
	}
	// Acquisition is complete. Its deadline must not retroactively cancel a
	// check while spending the separately allowed output budget.
	cancel()
	// Signals cancel acquisition/check, not the bounded attempt to report its
	// cancellation. One output deadline covers accepted, final and diagnostics.
	deadline := time.Now().Add(output)
	if max := start.Add(acquisition + output); deadline.After(max) {
		deadline = max
	}
	emit, stop := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer stop()
	if !t.Supported(false) {
		t.Diagnostic(emit, UnsupportedTransport)
		return 1
	}
	total := 0
	write := func(r Record) bool {
		b, err := Encode(r)
		if err != nil || len(b) > MaxRecord || total+len(b) > MaxOutput {
			return false
		}
		total += len(b)
		return t.Write(emit, b) == nil
	}
	deliveryFailed := func() int { t.Diagnostic(emit, OutputFailure); return 1 }
	if code != "" {
		if !write(failure(code)) {
			return deliveryFailed()
		}
		return code.ExitCode()
	}
	if mode == DiscoveryMode {
		if !write(discovery()) {
			return deliveryFailed()
		}
		return 0
	}
	// Acceptance is the first attempt to expose the executor-issued ID. Do not
	// produce any accepted record on a preaccept cancellation.
	if ctx.Err() != nil {
		code = contextCode(ctx)
		if !write(failure(code)) {
			return deliveryFailed()
		}
		return code.ExitCode()
	}
	if !write(accepted(operationID)) {
		return deliveryFailed()
	}
	if err := t.WriteFinal(emit, func() ([]byte, error) {
		// Selection must occur after output readiness, not before a possibly
		// long wait. The transport freezes these bytes on first progress.
		if ctx.Err() != nil {
			code = contextCode(ctx)
		}
		b, err := Encode(final(operationID, code))
		if err != nil || len(b) > MaxRecord || total+len(b) > MaxOutput {
			return nil, errCodec
		}
		return b, nil
	}); err != nil {
		return deliveryFailed()
	}
	return code.ExitCode()
}
