//go:build !darwin && !linux

package protocol

import (
	"context"
	"errors"
	"os"
)

type unsupportedTransport struct{}

func newSocketTransport(in, out, diagnostic *os.File) unsupportedTransport {
	return unsupportedTransport{}
}
func (unsupportedTransport) Supported(bool) bool { return false }
func (unsupportedTransport) ReadRequest(context.Context) ([]byte, Code) {
	return nil, UnsupportedTransport
}
func (unsupportedTransport) Write(context.Context, []byte) error {
	return errors.New("unsupported protocol transport")
}
func (unsupportedTransport) WriteFinal(context.Context, func() ([]byte, error)) error {
	return errors.New("unsupported protocol transport")
}
func (unsupportedTransport) Diagnostic(context.Context, Code) {}
func newID(context.Context) (string, Code)                    { return "", UnsupportedTransport }
