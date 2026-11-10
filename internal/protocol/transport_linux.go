//go:build linux

package protocol

import (
	"context"
	"encoding/hex"

	"golang.org/x/sys/unix"
)

const supportsSocketIO = true

func newID(ctx context.Context) (string, Code) {
	var b [16]byte
	for off := 0; off < len(b); {
		if ctx.Err() != nil {
			return "", contextCode(ctx)
		}
		// Never wait for early-boot entropy, and never fall back to a device file.
		n, err := unix.Getrandom(b[off:], unix.GRND_NONBLOCK)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			return "", InternalError
		}
		off += n
	}
	return "op_" + hex.EncodeToString(b[:]), ""
}
