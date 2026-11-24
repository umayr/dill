package orchestrator

import (
	"context"
	"testing"
)

func TestJobRejectsNamesBeforeEngineAccess(t *testing.T) {
	for name, engine := range map[string]JobEngine{"docker": &DockerEngine{}, "podman": &PodmanEngine{}} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if engine.StartJob(ctx, "service-name") == nil {
				t.Fatal("start accepted name")
			}
			if result, err := engine.WaitJob(ctx, "service-name"); err == nil || result.Known {
				t.Fatal("wait accepted name")
			}
			if engine.RemoveJob(ctx, "service-name") == nil {
				t.Fatal("remove accepted name")
			}
		})
	}
}
