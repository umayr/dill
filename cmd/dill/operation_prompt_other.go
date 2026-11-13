//go:build !darwin && !linux

package main

import (
	"context"
	"errors"
	"os"
)

func readOperationConfirmation(context.Context, *os.File) (string, error) {
	return "", errors.New("attended local operation input unavailable on this platform")
}
