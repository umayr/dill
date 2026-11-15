package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/umayr/dill/internal/operation"
	"github.com/umayr/dill/internal/orchestrator"
)

type preparedRestrictionSpy struct {
	orchestrator.Engine
	got   map[string]orchestrator.PreparedImage
	calls int
}

func (e *preparedRestrictionSpy) EnforcePreparedImages(images map[string]orchestrator.PreparedImage) error {
	e.calls++
	e.got = images
	return nil
}
func TestPreparedRestrictionQualifiedRecorderForwarding(t *testing.T) {
	images := map[string]orchestrator.PreparedImage{"app": {Reference: "synthetic:tag", ID: "sha256:" + strings.Repeat("a", 64)}}
	spy := &preparedRestrictionSpy{}
	// No recorder/target/session is necessary to install a restriction. Any
	// actual mutation still requires those independent existing authorities.
	engine := &operation.Engine{Engine: &coordinatedEngine{Engine: spy}}
	if err := engine.EnforcePreparedImages(images); err != nil || spy.calls != 1 || !reflect.DeepEqual(spy.got, images) {
		t.Fatal("qualified/recorder decorators dropped restriction", err)
	}
	if !errors.Is((&operation.Engine{}).EnforcePreparedImages(images), orchestrator.ErrDisposableUnavailable) {
		t.Fatal("missing recorder capability silently accepted")
	}
	if !errors.Is((&coordinatedEngine{}).EnforcePreparedImages(images), orchestrator.ErrDisposableUnavailable) {
		t.Fatal("missing qualified capability silently accepted")
	}
}
