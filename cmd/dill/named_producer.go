package main

import (
	"context"
	"sort"

	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"github.com/umayr/dill/internal/plan"
	"github.com/umayr/dill/internal/preflight"
)

// produceNamedCapture derives both review and handler intent from exactly the
// retained qualified observations. It never marks the result compared; only a
// fresh successful dispatcher comparison can authorize handler consumption.
func produceNamedCapture(ctx context.Context, inv preflight.Invocation, cfg *config.DillConfig, n *namedInventory, target preflight.Target, scope string) (*namedCapture, preflight.Selection, error) {
	return produceCapture(ctx, inv, cfg, n, target, scope, nil)
}
func produceCapture(ctx context.Context, inv preflight.Invocation, cfg *config.DillConfig, n *namedInventory, target preflight.Target, scope string, broker *brokersecret.Materialized) (*namedCapture, preflight.Selection, error) {
	var zero preflight.Selection
	bad := errNamedCaptureUnavailable
	if n == nil || cfg == nil || n.stack != inv.Stack {
		return nil, zero, bad
	}
	snapshot, err := n.snapshot(cfg)
	if err != nil {
		return nil, zero, err
	}
	input, err := preflight.ConfigInput(cfg, inv.Stack, inv.Operation, inv.EffectiveFlags(), n.prepared)
	if broker != nil {
		if inv.Operation != "up" {
			return nil, zero, bad
		}
		input, err = preflight.BrokerConfigInput(cfg, inv.Stack, inv.EffectiveFlags(), n.prepared, broker)
	}
	if err != nil {
		return nil, zero, err
	}
	inputDigest, err := input.Digest()
	if broker != nil {
		inputDigest, err = input.BrokerDigest()
	}
	if err != nil {
		return nil, zero, err
	}
	capture := &namedCapture{operation: inv.Operation, stack: inv.Stack, serviceIDs: map[string]string{}, stopIDs: map[string]string{}}
	changes := map[string]plan.ChangeKind{}
	switch inv.Operation {
	case "up":
		if broker == nil {
			capture.upPlan, err = plan.Compute(ctx, cfg, snapshot, inv.Stack)
		} else {
			capture.upPlan, err = plan.ComputeBroker(ctx, cfg, &brokerSnapshotEngine{namedSnapshotEngine: snapshot, endpoint: "unix://" + inv.Socket, engine: cfg.Engine}, inv.Stack, broker)
		}
		if err != nil {
			return nil, zero, err
		}
		for i := range capture.upPlan.Changes {
			ch := &capture.upPlan.Changes[i]
			if ch.Kind == plan.KindRemove {
				return nil, zero, bad
			}
			if inv.ForceRecreate && ch.Kind == plan.KindNoop {
				ch.Kind = plan.KindRecreate
			}
			changes[ch.Service] = ch.Kind
		}
		if len(changes) != len(cfg.Services) {
			return nil, zero, bad
		}
	case "start", "restart":
		if _, err = orchestrator.DirectStartIDs(ctx, snapshot, inv.Stack, cfg.Services, true); err != nil {
			return nil, zero, err
		}
	case "down":
		targets := make([]orchestrator.RemovalTarget, 0, len(cfg.Services))
		for service, svc := range cfg.Services {
			targets = append(targets, orchestrator.RemovalTarget{Name: serviceContainerName(inv.Stack, service, svc), Service: service})
		}
		capture.removal, err = orchestrator.CaptureRemovalIntent(ctx, snapshot, inv.Stack, targets)
		if err != nil {
			return nil, zero, err
		}
	case "stop":
	default:
		return nil, zero, bad
	}
	selection := preflight.Selection{LogicalContract: preflight.Version, Target: target, Stack: inv.Stack, Operation: inv.Operation, InputDigest: inputDigest, StateScope: scope, Resources: []preflight.Resource{}, Facts: preflight.Facts{Containers: []preflight.ContainerFact{}, Volumes: []preflight.VolumeFact{}}}
	ensuredVolumes := map[string]bool{}
	for service, svc := range cfg.Services {
		name := serviceContainerName(inv.Stack, service, svc)
		live, ok := n.containers[name]
		if !ok {
			return nil, zero, bad
		}
		row := preflight.Resource{Kind: "container", Name: name, Identity: name, IdentityKind: "planned-name", Presence: "absent", Retention: "not-applicable", Action: "none"}
		fact := preflight.ContainerFact{Name: name}
		if live != nil {
			digest, e := preflight.ContainerDigest(inv.Stack, service, name, live)
			if broker != nil {
				digest, e = preflight.BrokerContainerDigest(inv.Stack, service, name, live, cfg, broker)
			}
			if e != nil {
				return nil, zero, e
			}
			id := live.ID
			row.Identity, row.IdentityKind, row.Presence = id, "immutable-id", "present"
			fact.Exists, fact.ID, fact.ConfigDigest = true, &id, &digest
			capture.serviceIDs[service], capture.stopIDs[name] = id, id
			for _, mount := range live.Volumes {
				if broker != nil && mount.Type == "bind" {
					continue
				} // BrokerContainerDigest authenticated these exact files.
				resource, exists := n.resources["volume\x00"+mount.Source]
				if !exists || !resource.Exists {
					return nil, zero, bad
				}
			}
		} else {
			capture.serviceIDs[service] = ""
		}
		switch inv.Operation {
		case "up":
			switch changes[service] {
			case plan.KindCreate:
				if live != nil {
					return nil, zero, bad
				}
				row.Action = "create"
			case plan.KindRecreate:
				if live == nil {
					return nil, zero, bad
				}
				row.Action = "recreate"
			case plan.KindNoop:
				if live == nil {
					return nil, zero, bad
				}
				row.Action = "ensure-started"
			default:
				return nil, zero, bad
			}
			if row.Action == "create" || row.Action == "recreate" {
				mounts, e := svc.NormalizedVolumes()
				if e != nil {
					return nil, zero, e
				}
				for _, mount := range mounts {
					ensuredVolumes[mount.EngineName(inv.Stack)] = true
				}
			}
		case "start", "restart":
			if live == nil {
				return nil, zero, bad
			}
			row.Action = inv.Operation
		case "stop":
			if live != nil {
				row.Action = "stop"
			}
		case "down":
			if live != nil {
				row.Action = "remove"
			}
		}
		selection.Resources = append(selection.Resources, row)
		selection.Facts.Containers = append(selection.Facts.Containers, fact)
	}
	keys := make([]string, 0, len(n.resources))
	for key := range n.resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		resource := n.resources[key]
		row := preflight.Resource{Kind: resource.Kind, Name: resource.Name, Identity: resource.Name, IdentityKind: "planned-name", Presence: "absent", Retention: "preserve", Action: "retain"}
		if resource.Kind == "network" {
			if inv.Operation == "up" || inv.Operation == "start" || inv.Operation == "restart" {
				row.Action = "ensure"
			}
			fact := preflight.NetworkFact{Name: resource.Name}
			if resource.Exists {
				digest, e := preflight.NetworkDigest(inv.Stack, resource)
				if broker != nil {
					digest, e = preflight.BrokerResourceDigest(cfg.Engine, inv.Stack, resource)
				}
				if e != nil {
					return nil, zero, e
				}
				id := resource.ID
				fact.Exists, fact.ID, fact.SettingsDigest = true, &id, &digest
				row.Identity, row.IdentityKind, row.Presence = id, "immutable-id", "present"
			}
			selection.Facts.Network = fact
		} else if resource.Kind == "volume" {
			if ensuredVolumes[resource.Name] {
				row.Action = "ensure"
			}
			fact := preflight.VolumeFact{Name: resource.Name}
			if resource.Exists {
				digest, e := preflight.VolumeDigest(inv.Stack, resource)
				if broker != nil {
					digest, e = preflight.BrokerResourceDigest(cfg.Engine, inv.Stack, resource)
				}
				if e != nil {
					return nil, zero, e
				}
				fact.Exists, fact.SettingsDigest = true, &digest
				row.IdentityKind, row.Presence = "engine-name", "present"
			}
			selection.Facts.Volumes = append(selection.Facts.Volumes, fact)
		} else {
			return nil, zero, bad
		}
		selection.Resources = append(selection.Resources, row)
	}
	sort.Slice(selection.Resources, func(i, j int) bool {
		if selection.Resources[i].Kind != selection.Resources[j].Kind {
			return selection.Resources[i].Kind < selection.Resources[j].Kind
		}
		return selection.Resources[i].Name < selection.Resources[j].Name
	})
	selection.Coverage, err = preflight.CoverageFor(selection.Resources)
	if err != nil {
		return nil, zero, err
	}
	if broker == nil {
		_, err = selection.Digest()
	} else {
		_, err = (preflight.BrokerSelection{Contract: brokersecret.Version, Scope: broker.Scope(), Handle: broker.Review().Handle, Named: selection}).Digest()
	}
	if err != nil {
		return nil, zero, err
	}
	return capture, selection, nil
}
