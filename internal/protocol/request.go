package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	BootstrapVersion = "dill.bootstrap/1"
	Version          = "dill.dev/1"
	Operation        = "protocol.check"
	MaxRequest       = 4096
	MaxDepth         = 4
	MaxString        = 64
	MaxFeatures      = 8
	MaxRecord        = 4096
	MaxOutput        = 16 * 1024
)

type Request struct {
	ProtocolVersion  string   `json:"protocolVersion"`
	Operation        string   `json:"operation"`
	RequiredFeatures []string `json:"requiredFeatures"`
}

// Parse validates independently of Pkl and never returns input-derived errors.
func Parse(data []byte) (Request, Code) {
	var r Request
	if len(data) > MaxRequest {
		return r, RequestTooLarge
	}
	if !strictJSON(data, MaxDepth, MaxString) {
		return r, InvalidRequest
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 3 {
		return r, InvalidRequest
	}
	for _, k := range []string{"protocolVersion", "operation", "requiredFeatures"} {
		if _, ok := fields[k]; !ok {
			return r, InvalidRequest
		}
	}
	if json.Unmarshal(data, &r) != nil || len(r.RequiredFeatures) > MaxFeatures {
		return Request{}, InvalidRequest
	}
	seen := map[string]bool{}
	for _, f := range r.RequiredFeatures {
		if seen[f] {
			return Request{}, InvalidRequest
		}
		seen[f] = true
	}
	if r.ProtocolVersion != Version {
		return Request{}, UnsupportedProtocol
	}
	if r.Operation != Operation {
		return Request{}, UnsupportedOperation
	}
	for _, f := range r.RequiredFeatures {
		if f != "ndjson" && f != "resultAxes" {
			return Request{}, UnsupportedFeature
		}
	}
	return r, ""
}

// strictJSON rejects duplicate decoded keys before indexing and all nulls.
// Numbers use json.Number so codec validation can require exact integers.
func strictJSON(data []byte, maxDepth, maxString int) bool {
	if !utf8.Valid(data) || !validEscapes(data) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		tok, err := d.Token()
		if err != nil || tok == nil {
			return false
		}
		if s, ok := tok.(string); ok {
			return len(s) <= maxString
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		depth++
		if depth > maxDepth {
			return false
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return false
				}
				s, ok := k.(string)
				if !ok || len(s) > maxString || seen[s] {
					return false
				}
				seen[s] = true
				if !walk(depth) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !walk(depth) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		}
		return false
	}
	if !walk(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

// encoding/json replaces lone UTF-16 surrogates with U+FFFD. Reject them
// lexically instead; a literal valid U+FFFD is not itself an encoding error.
func validEscapes(b []byte) bool {
	in := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			in = !in
			continue
		}
		if !in || b[i] != '\\' {
			continue
		}
		i++
		if i >= len(b) {
			return false
		}
		if b[i] != 'u' {
			continue
		}
		if i+4 >= len(b) {
			return false
		}
		n, err := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
