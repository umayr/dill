//go:build darwin

package protocol

import "context"

// Darwin MSG_DONTWAIT does not prevent sosendcheck's capacity wait on a
// blocking socket (reproduced on Darwin 24.5). No documented independent
// per-call mechanism is established, so transport must fail closed here.
const supportsSocketIO = false

func newID(context.Context) (string, Code) { return "", UnsupportedTransport }
