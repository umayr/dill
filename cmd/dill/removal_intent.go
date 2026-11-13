package main

import (
	"fmt"
	"strings"

	"github.com/umayr/dill/internal/orchestrator"
)

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
