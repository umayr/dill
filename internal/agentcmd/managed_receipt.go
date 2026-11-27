package agentcmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/umayr/dill/internal/operation"
)

var errManagedReceiptFields = errors.New("ambiguous managed receipt fields")

// encoding/json deliberately accepts duplicate names and case-folded aliases.
// Neither is acceptable when a result may release a durable uncertainty fence.
// Validate the exact tagged shape recursively, then let the typed decoder check
// scalar types and the caller check semantic values. Field order is irrelevant.
func exactManagedReceiptFields(raw []byte, shape reflect.Type) error {
	nullable := shape.Kind() == reflect.Pointer || shape.Kind() == reflect.Slice
	for shape.Kind() == reflect.Pointer {
		shape = shape.Elem()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil
		}
		return errManagedReceiptFields
	}
	switch shape.Kind() {
	case reflect.Struct:
		d := json.NewDecoder(bytes.NewReader(raw))
		token, e := d.Token()
		if e != nil || token != json.Delim('{') {
			return errManagedReceiptFields
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < shape.NumField(); i++ {
			f := shape.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		seen := map[string]bool{}
		for d.More() {
			token, e = d.Token()
			if e != nil {
				return errManagedReceiptFields
			}
			name, ok := token.(string)
			field, known := fields[name]
			if !ok || !known || seen[name] {
				return errManagedReceiptFields
			}
			seen[name] = true
			var value json.RawMessage
			if d.Decode(&value) != nil || exactManagedReceiptFields(value, field) != nil {
				return errManagedReceiptFields
			}
		}
		token, e = d.Token()
		if e != nil || token != json.Delim('}') || d.Decode(new(any)) != io.EOF {
			return errManagedReceiptFields
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return errManagedReceiptFields
		}
		for _, value := range values {
			if exactManagedReceiptFields(value, shape.Elem()) != nil {
				return errManagedReceiptFields
			}
		}
	}
	return nil
}

// Start/stop/restart may change state, never immutable managed membership.
// Ordinary up is different: a successful update may replace resource IDs.
func sameManagedOwnership(a, b []operation.ManagedResource) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := make(map[operation.ManagedResource]bool, len(a))
	for _, r := range a {
		remaining[r] = true
	}
	for _, r := range b {
		if !remaining[r] {
			return false
		}
		delete(remaining, r)
	}
	return len(remaining) == 0
}
