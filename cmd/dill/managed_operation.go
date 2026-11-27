package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/preflight"
)

// Private adapter for the combined host agent. It does not bypass named
// preflight, inventory restrictions, or the retained coordination session.
func runManagedOperation(ctx context.Context, args []string) error {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, preflight.MaxOutputBytes+1))
	if err != nil || len(raw) > preflight.MaxOutputBytes {
		return protocolExit{2}
	}
	var expected operation.ManagedExpectation
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if len(args) == 0 || !namedBindingIntent(args) || dec.Decode(&expected) != nil || dec.Decode(new(any)) != io.EOF || !expected.Valid() ||
		((args[0] == "start" || args[0] == "stop" || args[0] == "restart") && expected.Mode != "update") ||
		(expected.Mode == "remove" && args[0] != "down") || (expected.Mode != "remove" && args[0] != "up" && args[0] != "start" && args[0] != "stop" && args[0] != "restart") {
		return protocolExit{2}
	}
	return runOperationWithOwnership(ctx, args, &expected)
}

func checkManagedSelection(expected operation.ManagedExpectation, resources []preflight.Resource) error {
	if !expected.Matches(resources) {
		return errNamedMismatch
	}
	return nil
}

func managedReceipt(ctx context.Context, s *operationState) ([]operation.ManagedResource, error) {
	c := s.coordination
	if c == nil || c.guard == nil || c.session == nil || s.named == nil || !s.named.compared {
		return nil, errNamedCaptureUnavailable
	}
	stack := s.named.stack
	listed, err := c.guard.ListStack(ctx, stack)
	if err != nil {
		return nil, err
	}
	var oldNetwork string
	for _, r := range s.managed.Resources {
		if r.Kind == "network" {
			oldNetwork = r.ID
		}
	}
	networks, err := c.guard.PreviewResources(ctx, stack, nil)
	if err != nil || len(networks) != 1 || networks[0].Kind != "network" || networks[0].Name != stack || !networks[0].Exists || (oldNetwork != "" && oldNetwork != networks[0].ID) {
		return nil, errNamedMismatch
	}
	if c.broker != nil {
		if _, err := preflight.BrokerResourceDigest(c.broker.Scope().Engine, stack, networks[0]); err != nil {
			return nil, err
		}
	} else if _, err := preflight.NetworkDigest(stack, networks[0]); err != nil {
		return nil, err
	}
	if s.managed.Mode == "remove" {
		if len(listed) != 0 {
			return nil, errNamedMismatch
		}
		return []operation.ManagedResource{}, nil
	}
	if len(listed) != len(s.config.Services) {
		return nil, errNamedMismatch
	}
	names := make(map[string]bool, len(listed))
	for _, name := range listed {
		if names[name] {
			return nil, errNamedMismatch
		}
		names[name] = true
	}
	created := map[string]bool{}
	var recorded operation.Result
	s.recorder.Finish(&recorded)
	if recorded.ResourcesTruncated {
		return nil, errNamedCaptureUnavailable
	}
	for _, r := range recorded.Resources {
		if r.Kind == "createScope" && r.Action == "create" && r.Effect == "complete" {
			created[r.IdentityDigest] = true
		}
	}
	rows := []operation.ManagedResource{{Kind: "network", Name: stack, ID: networks[0].ID}}
	for service, svc := range s.config.Services {
		name := serviceContainerName(stack, service, svc)
		live, err := c.guard.InspectConfig(ctx, name)
		if err != nil || live == nil || !names[name] || orchestrator.CheckOwnership(name, stack, service, live) != nil || !live.NetworksKnown || len(live.Networks) != 1 || live.Networks[0] != stack {
			return nil, errNamedMismatch
		}
		if c.broker != nil {
			if err := orchestrator.CheckBrokerMounts(service, live, c.broker); err != nil {
				return nil, err
			}
			review := c.broker.Review()
			if live.SystemLabels[orchestrator.SecretEnabledLabel] != "true" || live.SystemLabels[orchestrator.BrokerHandleLabel] != review.Handle || live.SystemLabels[orchestrator.BrokerGenerationLabel] != review.Scope.Generation {
				return nil, errNamedMismatch
			}
		}
		digest := operation.ResourceDigest("createScope", live.ID)
		if created[digest] {
			delete(created, digest)
		} else if s.named.serviceIDs[service] != live.ID {
			return nil, errNamedMismatch
		}
		rows = append(rows, operation.ManagedResource{Kind: "container", Name: name, ID: live.ID})
	}
	if len(created) != 0 || !(operation.ManagedExpectation{Mode: "update", Resources: rows}).Valid() || ctx.Err() != nil {
		return nil, errors.New("managed deployment evidence unavailable")
	}
	operation.SortManaged(rows)
	return rows, nil
}
