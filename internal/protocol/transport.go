package protocol

import (
	"context"
	"os"
)

// Transport owns no descriptors and starts no I/O workers. Implementations must
// honor context deadlines without closing/mutating borrowed descriptors.
type Transport interface {
	Supported(read bool) bool
	ReadRequest(context.Context) ([]byte, Code)
	Write(context.Context, []byte) error
	// Select the final immediately before each send attempt until its first
	// byte is sent; after any progress the selected bytes must remain frozen.
	WriteFinal(context.Context, func() ([]byte, error)) error
	Diagnostic(context.Context, Code)
}

func NewTransport(in, out, diagnostic *os.File) Transport {
	return newSocketTransport(in, out, diagnostic)
}
