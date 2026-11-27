package main

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/preflight"
)

type brokerSnapshotEngine struct {
	*namedSnapshotEngine
	endpoint string
	engine   string
}

func (e *brokerSnapshotEngine) CLIEndpoint() string { return e.endpoint }
func (e *brokerSnapshotEngine) EngineName() string  { return e.engine }

// This producer cannot capture or authorize live facts. Its caller supplies the
// exhaustive census held under the same qualified source/target/resource lease.
func produceBrokerCapture(ctx context.Context, inv preflight.Invocation, cfg *config.DillConfig, n *namedInventory, target preflight.Target, stateScope string, m *brokersecret.Materialized) (*namedCapture, preflight.BrokerSelection, error) {
	var zero preflight.BrokerSelection
	if m == nil || cfg == nil || cfg.Engine != m.Scope().Engine || inv.Engine != m.Scope().Engine || target.Engine != m.Scope().Engine || inv.Operation != "up" || brokersecret.Digest([]byte("unix://"+inv.Socket)) != m.Scope().EndpointDigest || target.QualifiedIdentityDigest != m.Scope().TargetDigest {
		return nil, zero, errNamedCaptureUnavailable
	}
	target.EndpointDigest = m.Scope().EndpointDigest
	capture, named, err := produceCapture(ctx, inv, cfg, n, target, stateScope, m)
	if err != nil {
		return nil, zero, err
	}
	selection := preflight.BrokerSelection{Contract: brokersecret.Version, Scope: m.Scope(), Handle: m.Review().Handle, Named: named}
	if _, err = selection.Digest(); err != nil {
		return nil, zero, err
	}
	return capture, selection, nil
}
