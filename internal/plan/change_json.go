package plan

import (
	"encoding/json"
	"errors"
)

// ChangeKind is emitted as a string by the native planner. Decode the same
// closed vocabulary at the agent boundary, never numeric or unknown defaults.
func (k *ChangeKind) UnmarshalJSON(raw []byte) error {
	var name string
	if k == nil || json.Unmarshal(raw, &name) != nil {
		return errors.New("invalid plan change kind")
	}
	var value ChangeKind
	switch name {
	case "create":
		value = KindCreate
	case "recreate":
		value = KindRecreate
	case "remove":
		value = KindRemove
	case "noop":
		value = KindNoop
	default:
		return errors.New("invalid plan change kind")
	}
	*k = value
	return nil
}
