//go:build !linux

package secretfile

import (
	"context"
	"testing"

	"github.com/umayr/dill/internal/config"
)

func TestUnsupportedPlatformFailsClosed(t *testing.T) {
	if err := Check(nil, Mapping{}); err != config.ErrSecretUnavailable {
		t.Fatalf("unsupported platform accepted: %v", err)
	}
	for _, authOnly := range []bool{false, true} {
		result, err := Preflight(context.Background(), Request{Config: selectionConfig(), AuthorizationOnly: authOnly})
		if result != nil || err != config.ErrSecretUnavailable {
			t.Fatalf("unsupported preflight accepted: %v", err)
		}
	}
	if _, err := runtimeMapping(context.Background(), "docker", "/run/docker.sock"); err != config.ErrSecretUnavailable {
		t.Fatal("unsupported runtime accepted")
	}
	if _, err := readPrivate("/private/sentinel", 0); err != config.ErrSecretUnavailable {
		t.Fatal("unsupported private loading accepted")
	}
	if _, err := checkSource(config.ResolvedSecret{HostPath: "/private/sentinel"}); err != config.ErrSecretUnavailable {
		t.Fatal("unsupported source accepted")
	}
}
