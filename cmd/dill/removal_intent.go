package main

import (
	"fmt"
	"strings"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

// Replace provisional operation census candidates with the exact handler-owned
// capture before either confirmation or dry-run output. Ordinary CLI is intact.
func (o commandOptions) replaceRemovalIntent(intent *orchestrator.RemovalIntent, action string, extra []operation.Intent) error {
	if o.operation == nil {
		return nil
	}
	intents := make([]operation.Intent, 0, len(intent.Resources)+len(extra))
	for _, resource := range intent.Resources {
		intents = append(intents, operation.Intent{Kind: "container", Identity: resource.ID, Action: action})
	}
	intents = append(intents, extra...)
	return o.operation.recorder.ReplaceIntents(intents)
}

func removalSummary(intent *orchestrator.RemovalIntent) string {
	resources := make([]string, 0, len(intent.Resources))
	for _, resource := range intent.Resources {
		resources = append(resources, fmt.Sprintf("%s (id %s)", resource.Name, resource.ID))
	}
	if len(resources) == 0 {
		return "no owned containers"
	}
	return strings.Join(resources, ", ")
}
