package brokersecret

import (
	"bytes"
	"encoding/json"
	"io"
)

// Reference is metadata only. Its handle is not authorization; every receiver
// reopens the private store and independently checks target/source/ownership.
const MaxMetadata = 1 << 20

type Reference struct {
	Scope  Scope  `json:"scope"`
	Handle string `json:"handle"`
}

func (r Reference) Valid() bool { return r.Scope.Valid() && ValidHandle(r.Handle) }
func DecodeMetadata(r io.Reader, dest any) error {
	if r == nil || dest == nil {
		return ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxMetadata+1))
	defer clear(raw)
	if err != nil || len(raw) > MaxMetadata || !uniqueJSON(raw) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(dest) != nil {
		return ErrInvalid
	}
	return nil
}
