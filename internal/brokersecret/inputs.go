package brokersecret

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Inputs carries NON-SECRET Pkl configuration variables in the immutable source
// snapshot. It is not part of a secret envelope. Only the BRINE_VAR_ namespace
// can reach the evaluator; target credentials/paths/process controls cannot.
const InputsFile = ".brine-broker-inputs.json"
const InputsVersion = "dill.broker-inputs/1"
const MaxInputs = 256 << 10

type Inputs struct {
	Contract  string            `json:"contract"`
	Variables map[string]string `json:"variables"`
}

var inputName = regexp.MustCompile(`^BRINE_VAR_[A-Z][A-Z0-9_]{0,63}$`)

func (i Inputs) Valid() bool {
	if i.Contract != InputsVersion || i.Variables == nil || len(i.Variables) > 256 {
		return false
	}
	size := 0
	for k, v := range i.Variables {
		if !inputName.MatchString(k) || strings.HasPrefix(strings.TrimPrefix(k, "BRINE_VAR_"), "BRINE_VAR_") || !utf8.ValidString(v) || strings.ContainsRune(v, 0) || len(v) > 16384 {
			return false
		}
		size += len(k) + len(v) + 2
	}
	return size <= 128<<10
}
func EncodeInputs(vars map[string]string) ([]byte, error) {
	i := Inputs{InputsVersion, vars}
	if !i.Valid() {
		return nil, ErrInvalid
	}
	b, err := json.Marshal(i)
	if err != nil || len(b) > MaxInputs {
		return nil, ErrInvalid
	}
	return b, nil
}
func DecodeInputs(r io.Reader) (Inputs, error) {
	var i Inputs
	b, err := io.ReadAll(io.LimitReader(r, MaxInputs+1))
	if err != nil || len(b) > MaxInputs || !utf8.Valid(b) {
		return Inputs{}, ErrInvalid
	}
	if DecodeMetadata(bytes.NewReader(b), &i) != nil || !i.Valid() {
		return Inputs{}, ErrInvalid
	}
	return i, nil
}
func (i Inputs) Environment() ([]string, error) {
	if !i.Valid() {
		return nil, ErrInvalid
	}
	keys := make([]string, 0, len(i.Variables))
	for k := range i.Variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, k := range keys {
		env = append(env, k+"="+i.Variables[k])
	}
	return env, nil
}
