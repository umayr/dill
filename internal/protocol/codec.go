package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

type WireError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}
type Record interface{ record() }
type Discovery struct {
	BootstrapVersion     string   `json:"bootstrapVersion"`
	RecordType           string   `json:"recordType"`
	ProtocolVersions     []string `json:"protocolVersions"`
	Operations           []string `json:"operations"`
	Features             []string `json:"features"`
	IRVersions           []string `json:"irVersions"`
	TargetCapabilities   []string `json:"targetCapabilities"`
	ProviderCapabilities []string `json:"providerCapabilities"`
}

func (Discovery) record() {}

type Accepted struct {
	ProtocolVersion string `json:"protocolVersion"`
	RecordType      string `json:"recordType"`
	OperationID     string `json:"operationId"`
	Operation       string `json:"operation"`
	Sequence        int    `json:"sequence"`
}

func (Accepted) record() {}

type Event struct {
	ProtocolVersion string `json:"protocolVersion"`
	RecordType      string `json:"recordType"`
	OperationID     string `json:"operationId"`
	Operation       string `json:"operation"`
	Sequence        int    `json:"sequence"`
	Event           string `json:"event"`
}

func (Event) record() {}

type Result struct {
	ProtocolVersion string      `json:"protocolVersion"`
	RecordType      string      `json:"recordType"`
	OperationID     string      `json:"operationId"`
	Operation       string      `json:"operation"`
	Sequence        int         `json:"sequence"`
	Outcome         string      `json:"outcome"`
	Effects         string      `json:"effects"`
	Cleanup         string      `json:"cleanup"`
	ChangesDetected bool        `json:"changesDetected"`
	Errors          []WireError `json:"errors"`
}

func (Result) record() {}

type BootstrapResult struct {
	BootstrapVersion string      `json:"bootstrapVersion"`
	RecordType       string      `json:"recordType"`
	Outcome          string      `json:"outcome"`
	Effects          string      `json:"effects"`
	Cleanup          string      `json:"cleanup"`
	ChangesDetected  bool        `json:"changesDetected"`
	Errors           []WireError `json:"errors"`
}

func (BootstrapResult) record() {}

func discovery() Discovery {
	return Discovery{BootstrapVersion, "discovery", []string{Version}, []string{Operation}, []string{"ndjson", "resultAxes"}, []string{}, []string{}, []string{}}
}
func accepted(id string) Accepted { return Accepted{Version, "accepted", id, Operation, 1} }
func wireErrors(code Code) []WireError {
	if code == "" {
		return []WireError{}
	}
	return []WireError{{code, code.Message()}}
}
func final(id string, code Code) Result {
	return Result{Version, "result", id, Operation, 2, code.outcome(), "none", "notNeeded", false, wireErrors(code)}
}
func failure(code Code) BootstrapResult {
	return BootstrapResult{BootstrapVersion, "result", code.outcome(), "none", "notNeeded", false, wireErrors(code)}
}

var errCodec = errors.New("invalid protocol record")

// Encode accepts only the closed record types and validates before exposing bytes.
func Encode(r Record) ([]byte, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, errCodec
	}
	b = append(b, '\n')
	if _, err := Decode(b); err != nil {
		return nil, errCodec
	}
	return b, nil
}

// Decode validates a single record, not production capability. Synthetic codec
// fixtures may represent partial/unknown outcomes never emitted by check.
func Decode(b []byte) (Record, error) {
	if len(b) > MaxRecord || len(b) == 0 || b[len(b)-1] != '\n' || bytes.Contains(b[:len(b)-1], []byte{'\n'}) || !strictJSON(b, 6, 128) {
		return nil, errCodec
	}
	var head map[string]json.RawMessage
	if json.Unmarshal(b, &head) != nil {
		return nil, errCodec
	}
	var kind string
	if json.Unmarshal(head["recordType"], &kind) != nil {
		return nil, errCodec
	}
	var dst any
	switch kind {
	case "discovery":
		dst = &Discovery{}
	case "accepted":
		dst = &Accepted{}
	case "event":
		dst = &Event{}
	case "result":
		if _, ok := head["bootstrapVersion"]; ok {
			dst = &BootstrapResult{}
		} else {
			dst = &Result{}
		}
	default:
		return nil, errCodec
	}
	// Require every field, exactly once with exact spelling. No optional/null bags.
	t := reflect.TypeOf(dst).Elem()
	if len(head) != t.NumField() {
		return nil, errCodec
	}
	for i := 0; i < t.NumField(); i++ {
		if _, ok := head[t.Field(i).Tag.Get("json")]; !ok {
			return nil, errCodec
		}
	}
	if raw, ok := head["errors"]; ok {
		var list []map[string]json.RawMessage
		if json.Unmarshal(raw, &list) != nil {
			return nil, errCodec
		}
		for _, item := range list {
			if len(item) != 2 || item["code"] == nil || item["message"] == nil {
				return nil, errCodec
			}
		}
	}
	if json.Unmarshal(b, dst) != nil {
		return nil, errCodec
	}
	switch v := dst.(type) {
	case *Discovery:
		if !reflect.DeepEqual(*v, discovery()) {
			return nil, errCodec
		}
		return *v, nil
	case *Accepted:
		if !validHeader(v.ProtocolVersion, v.OperationID, v.Operation, v.Sequence) || v.Sequence != 1 {
			return nil, errCodec
		}
		return *v, nil
	case *Event:
		if !validHeader(v.ProtocolVersion, v.OperationID, v.Operation, v.Sequence) || v.Sequence < 2 || v.Event != "checkProgress" {
			return nil, errCodec
		}
		return *v, nil
	case *Result:
		if !validHeader(v.ProtocolVersion, v.OperationID, v.Operation, v.Sequence) || v.Sequence < 2 || v.Outcome == "rejected" || !validAxes(v.Outcome, v.Effects, v.Cleanup, v.Errors) || !validAcceptedErrors(v.Outcome, v.Cleanup, v.Errors) {
			return nil, errCodec
		}
		return *v, nil
	case *BootstrapResult:
		if v.BootstrapVersion != BootstrapVersion || v.Effects != "none" || v.Cleanup != "notNeeded" || v.ChangesDetected || len(v.Errors) != 1 || !validAxes(v.Outcome, v.Effects, v.Cleanup, v.Errors) || !bootstrapCode(v.Errors[0].Code) || v.Outcome != v.Errors[0].Code.outcome() {
			return nil, errCodec
		}
		return *v, nil
	}
	return nil, errCodec
}
func validHeader(version, id, op string, seq int) bool {
	if version != Version || op != Operation || seq < 1 || seq > MaxOutput || len(id) != 35 || id[:3] != "op_" {
		return false
	}
	raw, err := hex.DecodeString(id[3:])
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == id[3:]
}
func validAxes(outcome, effects, cleanup string, errs []WireError) bool {
	if !oneOf(outcome, "succeeded", "rejected", "failed", "cancelled", "unknown") || !oneOf(effects, "none", "complete", "partial", "unknown") || !oneOf(cleanup, "notNeeded", "succeeded", "failed", "unknown") || errs == nil || len(errs) > 8 {
		return false
	}
	for _, e := range errs {
		msg, ok := messages[e.Code]
		if !ok || e.Message != msg {
			return false
		}
	}
	return (outcome != "succeeded" || len(errs) == 0) && (outcome != "rejected" || len(errs) > 0)
}

// Fixed messages are not enough: codes also carry phase/causality claims.
// OUTPUT_FAILURE is stderr-only and must never become a structured result.
func bootstrapCode(code Code) bool {
	switch code {
	case InvalidArguments, InvalidRequest, RequestTooLarge, UnsupportedProtocol, UnsupportedOperation, UnsupportedFeature, UnsupportedTransport, InternalError, Cancelled, DeadlineExceeded:
		return true
	default:
		return false
	}
}
func validAcceptedErrors(outcome, cleanup string, errs []WireError) bool {
	cancelled := false
	for _, e := range errs {
		switch e.Code {
		case InternalError:
			// Synthetic unknown completion and cleanup failure are independent
			// axes, not permission to admit preaccept rejection codes.
			if outcome != "failed" && outcome != "unknown" && !(outcome == "cancelled" && cleanup == "failed") {
				return false
			}
		case Cancelled, DeadlineExceeded:
			if outcome != "cancelled" {
				return false
			}
			cancelled = true
		default:
			return false
		}
	}
	return outcome != "cancelled" || cancelled
}
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// DecodeStream checks framing/causality; it makes no runtime success inference
// from a synthetic fixture, and never treats incomplete streams as success.
func DecodeStream(b []byte) ([]Record, error) {
	if len(b) > MaxOutput || len(b) == 0 || b[len(b)-1] != '\n' {
		return nil, errCodec
	}
	lines := bytes.Split(b[:len(b)-1], []byte{'\n'})
	var records []Record
	var id string
	ended := false
	for i, line := range lines {
		if ended {
			return nil, errCodec
		}
		r, err := Decode(append(append([]byte{}, line...), '\n'))
		if err != nil {
			return nil, errCodec
		}
		switch v := r.(type) {
		case Discovery, BootstrapResult:
			if i != 0 {
				return nil, errCodec
			}
			ended = true
		case Accepted:
			if i != 0 {
				return nil, errCodec
			}
			id = v.OperationID
		case Event:
			if id == "" || v.OperationID != id || v.Sequence != i+1 {
				return nil, errCodec
			}
		case Result:
			if id == "" || v.OperationID != id || v.Sequence != i+1 {
				return nil, errCodec
			}
			ended = true
		}
		records = append(records, r)
	}
	if !ended {
		return nil, errCodec
	}
	return records, nil
}
