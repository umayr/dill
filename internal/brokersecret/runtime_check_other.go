//go:build !linux

package brokersecret

func (m *Materialized) CheckRuntime(func(string, []Mount) error) error { return ErrUnsupported }
