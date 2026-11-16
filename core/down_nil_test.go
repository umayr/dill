package core_test

import (
	"context"
	"testing"

	"github.com/umayr/dill/core"
)

func TestDownRejectsNilDependencies(t *testing.T) {
	f := &downFake{}
	var typedNil *downFake
	tests := []struct {
		name   string
		ctx    context.Context
		engine core.DownEngine
	}{
		{"nil context", nil, f},
		{"nil engine", context.Background(), nil},
		{"typed nil engine", context.Background(), typedNil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := core.Down(tt.ctx, tt.engine, "stack", nil)
			if err == nil || len(result.Events) != 0 {
				t.Fatalf("result %#v, err %v", result, err)
			}
			if len(f.calls) != 0 {
				t.Fatalf("engine called: %v", f.calls)
			}
		})
	}
}
