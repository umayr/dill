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

func publishPrivate(string, []byte) (func(bool), error) { return nil, config.ErrSecretUnavailable }

func metadataIdentity(string) (fileID, error) { return fileID{}, config.ErrSecretUnavailable }

func samePrivateEntry(string, string) bool { return false }

func sourceMetadataIdentity(string) (fileID, error) {
	return fileID{}, config.ErrSecretUnavailable
}

func observeSource(config.ResolvedSecret) (sourceObservation, error) {
	return sourceObservation{}, config.ErrSecretUnavailable
}
