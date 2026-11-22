//go:build !darwin && !linux

package metricstransport

import (
	"errors"
	"os"
)

func readCredential(string, int64, []os.FileInfo) ([]byte, error) {
	return nil, errors.New("private credential loading is unavailable on this platform")
}
