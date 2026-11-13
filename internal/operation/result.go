// Package operation defines the bounded, local attended CLI result contract.
// It is independent of the metadata-only protocol and is not a transaction log.
package operation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
)

const Version = "dill.operation/1"

var Operations = []string{"validate", "plan", "diff", "up", "down", "stop", "start", "restart", "rm", "teardown"}

type Target struct {
	Engine         string `json:"engine"`
	EndpointDigest string `json:"endpointDigest"`
}

type Resource struct {
	Kind           string `json:"kind"`
	IdentityDigest string `json:"identityDigest"`
	Action         string `json:"action"`
	Effect         string `json:"effect"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Result struct {
	Version            string     `json:"version"`
	OperationID        string     `json:"operationId"`
	Operation          string     `json:"operation"`
	Target             *Target    `json:"target"`
	Stack              string     `json:"stack"`
	Outcome            string     `json:"outcome"`
	Effects            string     `json:"effects"`
	ChangesDetected    bool       `json:"changesDetected"`
	Resources          []Resource `json:"resources"`
	ResourcesTruncated bool       `json:"resourcesTruncated"`
	Errors             []Error    `json:"errors"`
}

// Digest avoids echoing caller-controlled identities, paths, image credentials,
// or engine error text. It is an identity token, NOT a confidentiality primitive
// for low-entropy secrets and NOT an authenticated daemon identity.
func Digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ResourceDigest domain-separates identity namespaces. Action is a separate
// field so stop/remove of the same container retain the same identity token.
func ResourceDigest(kind, identity string) string {
	return Digest("dill.resource/1\x00" + kind + "\x00" + identity)
}

// Recorder is safe for DAG goroutines. Begin is recorded before the engine
// call: an error or cancellation cannot prove that a daemon did not act.
// Its bounded resource list is not a recovery journal; counters never truncate.
type Recorder struct {
	mu                              sync.Mutex
	resources                       []Resource
	attempted, completed, uncertain int
	truncated, changes              bool
}

func (r *Recorder) Begin(kind, identity, action string) func(string, error) {
	r.mu.Lock()
	r.attempted++
	index := -1
	if len(r.resources) < 4096 {
		index = len(r.resources)
		r.resources = append(r.resources, Resource{kind, ResourceDigest(kind, identity), action, "unknown"})
	} else {
		r.truncated = true
	}
	r.uncertain++
	r.mu.Unlock()
	return func(resolved string, err error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if index >= 0 && resolved != "" {
			r.resources[index].IdentityDigest = ResourceDigest(kind, resolved)
		}
		if err == nil {
			r.completed++
			r.uncertain--
			if index >= 0 {
				r.resources[index].Effect = "complete"
			}
		}
	}
}

// Planned reports actions without raw config, field diffs, names or paths.
func (r *Recorder) Planned(identity, action string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if action != "noop" {
		r.changes = true
	}
	if len(r.resources) >= 4096 {
		r.truncated = true
		return
	}
	r.resources = append(r.resources, Resource{"service", ResourceDigest("service", identity), action, "none"})
}

func (r *Recorder) Finish(result *Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result.Resources = append([]Resource{}, r.resources...)
	sort.Slice(result.Resources, func(i, j int) bool {
		a, b := result.Resources[i], result.Resources[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.IdentityDigest != b.IdentityDigest {
			return a.IdentityDigest < b.IdentityDigest
		}
		if a.Action != b.Action {
			return a.Action < b.Action
		}
		return a.Effect < b.Effect
	})
	result.ResourcesTruncated = r.truncated
	result.ChangesDetected = r.changes
	result.Effects = "none"
	if r.uncertain > 0 {
		result.Effects = "unknown"
	} else if r.attempted > 0 {
		result.Effects = "complete"
		if result.Outcome != "succeeded" {
			result.Effects = "partial"
		}
	}
}
