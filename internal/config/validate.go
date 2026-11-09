package config

import "context"

// Validate evaluates and validates a configuration using the same path as Load.
func Validate(ctx context.Context, path string) error {
	_, err := Load(ctx, path)
	return err
}
