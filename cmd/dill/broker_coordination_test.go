package main

import (
	"context"
	"github.com/umayr/dill/internal/brokersecret"
	"github.com/umayr/dill/internal/config"
	"github.com/umayr/dill/internal/orchestrator"
	"testing"
)

func TestBrokerCoordinationRejectsZeroGenerationBeforeTargetAccess(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker", Services: map[string]*config.Service{"api": {Image: "fixture", PullPolicy: "never"}}}
	c := &operationCoordination{broker: &brokersecret.Materialized{}}
	if err := c.prepare(context.Background(), commandOptions{}, cfg, "up", "never"); err == nil || c.target != nil || c.state != nil || c.session != nil {
		t.Fatal("invalid broker evidence reached target or state")
	}
}
func TestBrokerUpCannotBypassComparedCapture(t *testing.T) {
	cfg := &config.DillConfig{Name: "app", Engine: "docker"}
	opts := commandOptions{operation: &operationState{coordination: &operationCoordination{broker: &brokersecret.Materialized{}}}}
	opened := false
	err := runUpConfig(context.Background(), opts, cfg, nil, false, "never", func(context.Context, *config.DillConfig) (orchestrator.Engine, error) { opened = true; return nil, nil })
	if err == nil || opened {
		t.Fatal("broker up without retained compared capture")
	}
}
