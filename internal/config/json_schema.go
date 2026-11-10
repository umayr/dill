package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
)

func sortedJSONKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Nullable fields mirror the current dill.pkl, independently of Go zero values.
func nullableSchemaField(t reflect.Type, name string) bool {
	switch t {
	case reflect.TypeOf(DillConfig{}):
		return name == "name"
	case reflect.TypeOf(Service{}):
		return name != "image" && name != "restart" && name != "pullPolicy"
	case reflect.TypeOf(Port{}):
		return name == "published" || name == "hostIp"
	case reflect.TypeOf(Mount{}):
		return name == "source" || name == "name"
	case reflect.TypeOf(HealthCheck{}):
		return name != "test"
	}
	return false
}

func requireJSONFields(raw json.RawMessage, fields ...string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("expected object")
	}
	for _, field := range fields {
		if _, ok := m[field]; !ok {
			return fmt.Errorf("%s: required value is missing", field)
		}
	}
	return nil
}
