// Package protocol implements only the experimental metadata-only foundation.
// It has no dependency on configuration, Pkl, engines, targets or providers.
package protocol

import (
	"context"
	"errors"
)

func contextCode(ctx context.Context) Code {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return DeadlineExceeded
	}
	return Cancelled
}

// Code is a closed, non-input-bearing diagnostic vocabulary.
type Code string

const (
	InvalidArguments     Code = "INVALID_ARGUMENTS"
	InvalidRequest       Code = "INVALID_REQUEST"
	RequestTooLarge      Code = "REQUEST_TOO_LARGE"
	UnsupportedProtocol  Code = "UNSUPPORTED_PROTOCOL"
	UnsupportedOperation Code = "UNSUPPORTED_OPERATION"
	UnsupportedFeature   Code = "UNSUPPORTED_FEATURE"
	UnsupportedTransport Code = "UNSUPPORTED_TRANSPORT"
	InternalError        Code = "INTERNAL_ERROR"
	Cancelled            Code = "CANCELLED"
	DeadlineExceeded     Code = "DEADLINE_EXCEEDED"
	OutputFailure        Code = "OUTPUT_FAILURE"
)

var messages = map[Code]string{
	InvalidArguments:     "Invalid protocol command arguments.",
	InvalidRequest:       "Invalid protocol request.",
	RequestTooLarge:      "Protocol request exceeds the size limit.",
	UnsupportedProtocol:  "Unsupported protocol. Use discovery to select a supported version.",
	UnsupportedOperation: "Unsupported operation. Only protocol.check is available.",
	UnsupportedFeature:   "A required protocol feature is unavailable.",
	UnsupportedTransport: "Protocol transport is unavailable. Use Unix stream sockets on Linux.",
	InternalError:        "Protocol check could not complete.",
	Cancelled:            "Protocol check was cancelled.",
	DeadlineExceeded:     "Protocol check exceeded its deadline.",
	OutputFailure:        "Protocol output could not be delivered.",
}

func (c Code) Message() string {
	if s, ok := messages[c]; ok {
		return s
	}
	return messages[InternalError]
}
func (c Code) ExitCode() int {
	switch c {
	case "":
		return 0
	case InvalidArguments, InvalidRequest, RequestTooLarge, UnsupportedProtocol, UnsupportedOperation, UnsupportedFeature:
		return 2
	default:
		return 1
	}
}
func (c Code) outcome() string {
	if c == "" {
		return "succeeded"
	}
	if c == Cancelled || c == DeadlineExceeded {
		return "cancelled"
	}
	if c.ExitCode() == 2 {
		return "rejected"
	}
	return "failed"
}
