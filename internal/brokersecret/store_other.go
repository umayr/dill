//go:build !linux

package brokersecret

type Store struct{}

func (*Store) activeReview(string, Scope) (Review, error) { return Review{}, ErrUnsupported }

func Provision(string) error                                    { return ErrUnsupported }
func Open(string) (*Store, error)                               { return nil, ErrUnsupported }
func (*Store) Close() error                                     { return nil }
func (*Store) PrepareRetirement(Retirement) (Review, error)     { return Review{}, ErrUnsupported }
func (*Store) Prepare(Envelope, Scope, []Grant) (Review, error) { return Review{}, ErrUnsupported }
func (*Store) Resolve(string, Scope) (*Materialized, error)     { return nil, ErrUnsupported }
func (*Store) Activate(string, Scope) (*Materialized, error)    { return nil, ErrUnsupported }
