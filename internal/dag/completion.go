package dag

import (
	"context"
	"fmt"
	"github.com/umayr/dill/internal/orchestrator"
)

type exitWaiter interface {
	WaitJob(context.Context, string) (orchestrator.JobResult, error)
}

func waitSuccessfulExit(ctx context.Context, e orchestrator.Engine, id string, n *node) error {
	r, err := e.(exitWaiter).WaitJob(ctx, id)
	if err != nil {
		return fmt.Errorf("waiting for %s to exit: %w", n.name, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.Known {
		return fmt.Errorf("unknown exit status for %s", n.name)
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("dependency %s exited with status %d", n.name, r.ExitCode)
	}
	close(n.completed)
	return nil
}
