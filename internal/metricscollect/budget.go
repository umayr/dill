package metricscollect

import (
	"context"
	"errors"
	"io"
	"time"
)

// Errors mark attempts beyond the shared ceilings. The acquisition is
// unavailable even if a source swallows either error.
var (
	ErrReadBudget = errors.New("metrics upstream read budget exhausted")
	ErrCallBudget = errors.New("metrics upstream call budget exhausted")
)

type upstreamBudget struct {
	bytesLeft int
	callsLeft int
	breach    error
}

// ReadBudget is issued for a single source invocation, with shared call and
// byte allowances across the whole acquisition. Do not use it concurrently
// or retain it or its readers after the invocation.
//
// Put each upstream operation through Do and apply Reader before buffering
// or decoding each body/read. Failed attempts and bytes read with an error
// still count. These gates cannot bound hidden SDK/transport work, framing,
// decompression, or server-side work.
type ReadBudget struct {
	ctx      context.Context
	upstream *upstreamBudget
}

// Do admits one synchronous upstream operation with the source's deadline.
// An operation is one physical request or one local open/read/close. It must
// not retry, redirect, batch multiple requests, or start detached work. A
// source that cannot put all upstream operations through this gate is not
// qualified. On refusal fn is not called. Cleanup needs no admission and
// must not perform new upstream operations.
func (b ReadBudget) Do(fn func(context.Context) error) error {
	if b.upstream == nil {
		return ErrCallBudget
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if !beforeDeadline(b.ctx, time.Now()) {
		return context.DeadlineExceeded
	}
	if b.upstream.breach != nil {
		return b.upstream.breach
	}
	if b.upstream.callsLeft == 0 {
		b.upstream.breach = ErrCallBudget
		return ErrCallBudget
	}
	b.upstream.callsLeft--
	return fn(b.ctx)
}

// Reader wraps the original reader without reading ahead. At the ceiling it
// fails without probing for EOF; a prefix at the ceiling is not proof of a
// complete body. The source is still responsible for closing the original.
func (b ReadBudget) Reader(r io.Reader) io.Reader { return meteredReader{budget: b, upstream: r} }

type meteredReader struct {
	budget   ReadBudget
	upstream io.Reader
}

func (r meteredReader) Read(p []byte) (int, error) {
	b := r.budget
	if b.upstream == nil {
		return 0, ErrReadBudget
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if !beforeDeadline(b.ctx, time.Now()) {
		return 0, context.DeadlineExceeded
	}
	if len(p) == 0 {
		return 0, nil
	}
	if b.upstream.breach != nil {
		return 0, b.upstream.breach
	}
	if b.upstream.bytesLeft == 0 {
		b.upstream.breach = ErrReadBudget
		return 0, ErrReadBudget
	}
	p = p[:min(len(p), b.upstream.bytesLeft)]
	n, err := r.upstream.Read(p)
	b.upstream.bytesLeft -= n
	if n == 0 && err == nil {
		return 0, io.ErrNoProgress
	}
	return n, err
}
