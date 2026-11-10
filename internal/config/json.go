package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// rejectDuplicateKeys walks the complete JSON stream before any map decoding.
func rejectDuplicateKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(string) error
	walk = func(path string) error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("%s: expected object key", path)
				}
				if seen[key] {
					return fmt.Errorf("%s: duplicate key %q", path, key)
				}
				seen[key] = true
				if err := walk(path + "[" + fmt.Sprintf("%q", key) + "]"); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for i := 0; d.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		default:
			return fmt.Errorf("%s: unexpected delimiter %q", path, delim)
		}
	}
	if err := walk("$"); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

// decodeStrict requires exact schema property names, including nested objects.
// Maps intentionally accept arbitrary keys. Raw union values are checked by
// their respective normalizers after decoding.
func decodeStrict(data []byte, out any) error {
	var check func(json.RawMessage, reflect.Type, string, bool) error
	check = func(raw json.RawMessage, t reflect.Type, path string, nullable bool) error {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if nullable {
				return nil
			}
			return fmt.Errorf("%s: null is not allowed", path)
		}
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if t == rawMessageType {
			return nil
		}
		switch t.Kind() {
		case reflect.Struct:
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			allowed := map[string]reflect.Type{}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name != "" && name != "-" {
					allowed[name] = f.Type
				}
			}
			names := sortedJSONKeys(fields)
			for _, name := range names {
				value := fields[name]
				ft, ok := allowed[name]
				if !ok {
					return fmt.Errorf("%s: unknown property %q", path, name)
				}
				if err := check(value, ft, path+"."+name, nullableSchemaField(t, name)); err != nil {
					return err
				}
			}
		case reflect.Map:
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			for _, name := range sortedJSONKeys(fields) {
				if err := check(fields[name], t.Elem(), path+"["+fmt.Sprintf("%q", name)+"]", false); err != nil {
					return err
				}
			}
		case reflect.Slice:
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			for i, value := range values {
				if err := check(value, t.Elem(), fmt.Sprintf("%s[%d]", path, i), false); err != nil {
					return err
				}
			}
		default:
			if err := json.Unmarshal(raw, reflect.New(t).Interface()); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		return nil
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("decode destination must be a pointer")
	}
	if err := check(data, t.Elem(), "config", false); err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
