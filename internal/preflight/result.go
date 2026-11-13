// Package preflight defines the explicitly negotiated named-resource review
// contract. A review is not a reservation, authorization or transaction.
package preflight

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

const (
	Version             = "dill.preflight/1"
	CapabilitiesVersion = "dill.preflight-capabilities/1"
	MaxResources        = 256
	MaxNameBytes        = 255
	MaxOutputBytes      = 262144
)

var (
	namePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	idPattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
	operationIDPattern = regexp.MustCompile(`^op_[0-9a-f]{32}$`)
)

func Operations() []string { return []string{"up", "start", "restart", "stop", "down"} }
func SupportedOperation(s string) bool {
	for _, op := range Operations() {
		if s == op {
			return true
		}
	}
	return false
}
func ValidName(s string) bool   { return len(s) <= MaxNameBytes && namePattern.MatchString(s) }
func ValidDigest(s string) bool { return digestPattern.MatchString(s) }
func ValidID(s string) bool     { return idPattern.MatchString(s) }

type Limits struct {
	MaxResources   int `json:"maxResources"`
	MaxNameBytes   int `json:"maxNameBytes"`
	MaxOutputBytes int `json:"maxOutputBytes"`
}
type Capabilities struct {
	Version       string   `json:"version"`
	Supported     bool     `json:"supported"`
	Contract      string   `json:"contract,omitempty"`
	ApplyContract string   `json:"applyContract,omitempty"`
	Operations    []string `json:"operations,omitempty"`
	Engines       []string `json:"engines,omitempty"`
	Layouts       []string `json:"layouts,omitempty"`
	Limits        *Limits  `json:"limits,omitempty"`
}

func Discovery(supported bool) Capabilities {
	c := Capabilities{Version: CapabilitiesVersion, Supported: supported}
	if supported {
		c.Contract, c.ApplyContract = Version, "dill.operation/1"
		c.Operations, c.Engines = Operations(), []string{"docker"}
		c.Layouts = []string{"native-local-docker-v1"}
		c.Limits = &Limits{MaxResources, MaxNameBytes, MaxOutputBytes}
	}
	return c
}

type Target struct {
	Engine                  string `json:"engine"`
	EndpointDigest          string `json:"endpointDigest"`
	QualifiedIdentityDigest string `json:"qualifiedIdentityDigest"`
}

func (t *Target) Valid() bool {
	return t != nil && t.Engine == "docker" && ValidDigest(t.EndpointDigest) && ValidDigest(t.QualifiedIdentityDigest)
}

type Scope struct {
	Scope        string `json:"scope"`
	Count        int    `json:"count"`
	PresentCount int    `json:"presentCount"`
	Complete     bool   `json:"complete"`
}
type Coverage struct {
	Containers Scope `json:"containers"`
	Network    Scope `json:"network"`
	Volumes    Scope `json:"volumes"`
}

func EmptyCoverage() Coverage {
	return Coverage{Containers: Scope{Scope: "declared-stack"}, Network: Scope{Scope: "stack-network"}, Volumes: Scope{Scope: "declared-attached-named"}}
}

type Resource struct {
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Identity     string `json:"identity"`
	IdentityKind string `json:"identityKind"`
	Presence     string `json:"presence"`
	Action       string `json:"action"`
	Retention    string `json:"retention"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var messages = map[string]string{
	"INVALID_ARGUMENTS":           "Invalid preflight arguments.",
	"UNSUPPORTED_OPERATION":       "Unsupported preflight operation.",
	"UNSUPPORTED_CONTRACT":        "Unsupported preflight contract.",
	"UNSUPPORTED_FORMAT":          "Unsupported preflight format.",
	"INVALID_TARGET":              "Explicit local Docker target and stack are required.",
	"UNSUPPORTED_PREFLIGHT":       "Unsupported qualified preflight invocation.",
	"SECRET_UNAVAILABLE":          "Secret configuration or source unavailable.",
	"EVALUATION_FAILED":           "Configuration evaluation failed.",
	"CONFIG_INVALID":              "Configuration is invalid.",
	"QUALIFICATION_FAILED":        "Qualified target unavailable.",
	"COORDINATION_UNAVAILABLE":    "Trusted coordination state unavailable. Inspect before retrying.",
	"PREFLIGHT_UNAVAILABLE":       "Complete preflight evidence unavailable.",
	"PREFLIGHT_LIMIT_EXCEEDED":    "Preflight response exceeds supported limits.",
	"CANCELLED":                   "Preflight cancelled or deadline exceeded.",
	"ENGINE_CLOSE_FAILED":         "Preflight transport cleanup failed.",
	"COORDINATION_COMMIT_FAILED":  "Durable preflight outcome unavailable. Inspect before retrying.",
	"COORDINATION_RELEASE_FAILED": "Preflight coordination cleanup failed. Inspect before retrying.",
}

// FixedError never reflects a caller, evaluator or engine diagnostic.
func FixedError(code string) Error {
	message, ok := messages[code]
	if !ok {
		code, message = "PREFLIGHT_UNAVAILABLE", messages["PREFLIGHT_UNAVAILABLE"]
	}
	return Error{code, message}
}

type Result struct {
	Version             string     `json:"version"`
	OperationID         string     `json:"operationId"`
	Operation           string     `json:"operation"`
	Target              *Target    `json:"target"`
	Stack               string     `json:"stack"`
	Outcome             string     `json:"outcome"`
	EngineEffects       string     `json:"engineEffects"`
	CoordinationEffects string     `json:"coordinationEffects"`
	InputDigest         *string    `json:"inputDigest"`
	SelectionDigest     *string    `json:"selectionDigest"`
	Coverage            Coverage   `json:"coverage"`
	Resources           []Resource `json:"resources"`
	ResourcesTruncated  bool       `json:"resourcesTruncated"`
	Errors              []Error    `json:"errors"`
}

func NewResult(id, operation string) Result {
	return Result{Version: Version, OperationID: id, Operation: operation, Outcome: "invalid_input", EngineEffects: "none", CoordinationEffects: "none", Coverage: EmptyCoverage(), Resources: []Resource{}, Errors: []Error{}}
}

// Withdraw clears ALL review material, never an apparently usable prefix.
func (r *Result) Withdraw() {
	r.InputDigest, r.SelectionDigest = nil, nil
	r.Resources = []Resource{}
	r.Coverage = EmptyCoverage()
}

func CoverageFor(resources []Resource) (Coverage, error) {
	c := EmptyCoverage()
	seen := map[string]bool{}
	if len(resources) > MaxResources {
		return c, errors.New("preflight resource limit")
	}
	for _, r := range resources {
		key := r.Kind + "\x00" + r.Name
		if seen[key] {
			return EmptyCoverage(), errors.New("duplicate preflight resource")
		}
		seen[key] = true
		var scope *Scope
		switch r.Kind {
		case "container":
			scope = &c.Containers
		case "network":
			scope = &c.Network
		case "volume":
			scope = &c.Volumes
		default:
			return EmptyCoverage(), errors.New("unknown preflight kind")
		}
		scope.Count++
		if r.Presence == "present" {
			scope.PresentCount++
		}
	}
	c.Containers.Complete, c.Network.Complete, c.Volumes.Complete = true, true, true
	return c, nil
}

func (r Resource) validate(operation, stack string) error {
	if !ValidName(r.Name) {
		return errors.New("unsupported preflight name")
	}
	switch r.Presence {
	case "present":
		if r.Kind == "volume" {
			if r.IdentityKind != "engine-name" || r.Identity != r.Name {
				return errors.New("invalid volume identity")
			}
		} else if r.IdentityKind != "immutable-id" || !ValidID(r.Identity) {
			return errors.New("invalid inspected identity")
		}
	case "absent":
		if r.IdentityKind != "planned-name" || r.Identity != r.Name {
			return errors.New("invalid absent identity")
		}
	default:
		return errors.New("invalid preflight presence")
	}
	switch r.Kind {
	case "container":
		if r.Retention != "not-applicable" {
			return errors.New("invalid container retention")
		}
		present := r.Presence == "present"
		valid := false
		switch operation {
		case "up":
			valid = (!present && r.Action == "create") || (present && (r.Action == "recreate" || r.Action == "ensure-started"))
		case "start", "restart":
			valid = present && r.Action == operation
		case "stop":
			valid = (present && r.Action == "stop") || (!present && r.Action == "none")
		case "down":
			valid = (present && r.Action == "remove") || (!present && r.Action == "none")
		}
		if !valid {
			return errors.New("invalid container action")
		}
	case "network":
		if r.Name != stack || r.Retention != "preserve" {
			return errors.New("invalid network scope")
		}
		action := "ensure"
		if operation == "stop" || operation == "down" {
			action = "retain"
		}
		if r.Action != action {
			return errors.New("invalid network action")
		}
	case "volume":
		if r.Retention != "preserve" || (r.Action != "retain" && !(operation == "up" && r.Action == "ensure")) {
			return errors.New("invalid volume action")
		}
	default:
		return errors.New("invalid resource kind")
	}
	return nil
}

// Validate checks producer-side invariants. It is not a permissive JSON decoder:
// consumers must separately reject missing/null/duplicate/unknown wire keys.
func (r Result) Validate(exit int) error {
	if r.Version != Version || !operationIDPattern.MatchString(r.OperationID) || r.EngineEffects != "none" {
		return errors.New("invalid preflight envelope")
	}
	if r.Target != nil && !r.Target.Valid() {
		return errors.New("unqualified preflight target")
	}
	if r.Operation != "" && !SupportedOperation(r.Operation) {
		return errors.New("invalid preflight operation")
	}
	if r.Stack != "" && !ValidName(r.Stack) {
		return errors.New("invalid preflight stack")
	}
	if r.CoordinationEffects != "none" && r.CoordinationEffects != "written" && r.CoordinationEffects != "unknown" {
		return errors.New("invalid metadata effects")
	}
	if r.Resources == nil || r.Errors == nil || len(r.Errors) > 4 {
		return errors.New("invalid preflight arrays")
	}
	limitFailure := false
	for _, e := range r.Errors {
		if message, ok := messages[e.Code]; !ok || message != e.Message {
			return errors.New("unclassified preflight diagnostic")
		}
		if e.Code == "PREFLIGHT_LIMIT_EXCEEDED" {
			limitFailure = true
		}
		if e.Code == "SECRET_UNAVAILABLE" && (r.Target != nil || r.Stack != "") {
			return errors.New("secret failure disclosed identifying target")
		}
	}
	if r.ResourcesTruncated != limitFailure {
		return errors.New("invalid preflight overflow marker")
	}
	if r.Outcome == "succeeded" {
		if exit != 0 || !SupportedOperation(r.Operation) || !r.Target.Valid() || !ValidName(r.Stack) || r.CoordinationEffects != "written" || r.ResourcesTruncated || len(r.Errors) != 0 || r.InputDigest == nil || r.SelectionDigest == nil || !ValidDigest(*r.InputDigest) || !ValidDigest(*r.SelectionDigest) {
			return errors.New("incomplete preflight success")
		}
		for _, resource := range r.Resources {
			if err := resource.validate(r.Operation, r.Stack); err != nil {
				return err
			}
		}
		coverage, err := CoverageFor(r.Resources)
		if err != nil {
			return err
		}
		if coverage != r.Coverage || coverage.Network.Count != 1 {
			return errors.New("invalid preflight coverage")
		}
	} else {
		if exit != 1 && exit != 2 {
			return errors.New("invalid preflight failure exit")
		}
		if r.Outcome != "failed" && r.Outcome != "cancelled" && r.Outcome != "unknown" && r.Outcome != "invalid_input" {
			return errors.New("invalid preflight outcome")
		}
		if (exit == 2) != (r.Outcome == "invalid_input") || len(r.Errors) == 0 || len(r.Resources) != 0 || r.InputDigest != nil || r.SelectionDigest != nil || r.Coverage != EmptyCoverage() {
			return errors.New("preflight failure disclosed review")
		}
	}
	return nil
}

// Buffer fully serializes a candidate WITHOUT writing stdout. Callers must make
// the byte-limit decision before durable terminal publication. Only emit these
// bytes after all required finalization succeeds; otherwise withdraw and emit
// a separately bounded fixed failure, preserving any durable terminal fact.
func (r Result) Buffer(exit int) ([]byte, error) {
	if err := r.Validate(exit); err != nil {
		return nil, err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if len(b)+1 > MaxOutputBytes {
		return nil, fmt.Errorf("preflight output limit")
	}
	return append(b, '\n'), nil
}
