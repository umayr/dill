package core

import (
	"context"
	"testing"
)

func TestScopedPlatformValidation(t *testing.T) {
	if err := validateScopedPlatform(nil, "linux"); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := validateScopedPlatform(context.Background(), "windows"); err == nil {
		t.Fatal("Windows accepted")
	}
	if err := validateScopedPlatform(context.Background(), "linux"); err != nil {
		t.Fatal(err)
	}
	if err := validateScopedPlatform(context.Background(), "darwin"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigWithVars(nil, "unused", nil); err == nil {
		t.Fatal("nil context accepted by API")
	}
}
