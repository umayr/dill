package orchestrator

import (
	"context"
	"encoding/json"
	"sort"
)

// PreviewResource is private stable resource evidence. Settings deliberately
// excludes usage/reference counts, attached containers and update timestamps.
type PreviewResource struct {
	Kind, Name, ID string
	Exists         bool
	Settings       json.RawMessage
}

type PreviewResourceInspector interface {
	PreviewResources(context.Context, string, []string) ([]PreviewResource, error)
}

func previewNames(names []string) []string {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	out := []string{}
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
