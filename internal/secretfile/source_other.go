//go:build !linux

package secretfile

import (
	"context"

	"github.com/umayr/dill/internal/config"
)

func supportedPlatform() bool { return false }

func readPrivate(string, uint32) ([]byte, error) { return nil, config.ErrSecretUnavailable }
func runtimeMapping(context.Context, string, string) (*runtimeEvidence, error) {
	return nil, config.ErrSecretUnavailable
}

func checkSource(config.ResolvedSecret) (fileID, error) { return fileID{}, config.ErrSecretUnavailable }
