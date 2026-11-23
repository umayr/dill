package metricscollect

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrReadBudget marks an attempted read beyond the shared byte ceiling. The
// acquisition is unavailable even if a source swallows this error.
var ErrReadBudget = errors.New("metrics upstream read budget exhausted")

type byteBudget struct {
	remaining int
	exhausted bool
}

// ReadBudget is issued for a single source invocation, with a shared byte
// allowance across all invocations in one acquisition. It and its readers
// must not be used concurrently or retained after the invocation.
//
// Apply Reader before buffering or decoding each body/read. Even bytes read
// with an error or from an unsuccessful attempt consume the allowance. Native
// request count, SDK/transport buffering, framing, decompression, and work on
// the server are separate concerns; this reader cannot bound them.
type ReadBudget struct {
	ctx   context.Context
	bytes *byteBudget
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
	if b.bytes == nil {
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
	if b.bytes.remaining == 0 {
		b.bytes.exhausted = true
		return 0, ErrReadBudget
	}
	p = p[:min(len(p), b.bytes.remaining)]
	n, err := r.upstream.Read(p)
	b.bytes.remaining -= n
	return n, err
}
