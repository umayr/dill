package main

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/preflight"
	"testing"
)

func TestBrokerCaptureRequiresTargetBoundOpaqueEvidence(t *testing.T) {
	for _, m := range []*brokersecret.Materialized{nil, {}} {
		_, _, err := produceBrokerCapture(context.Background(), preflight.Invocation{Operation: "up", Socket: "/socket"}, nil, nil, preflight.Target{}, "", m)
		if err == nil {
			t.Fatal("capture accepted unqualified target evidence")
		}
	}
}
