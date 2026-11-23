package orchestrator

import (
	"context"
	"errors"
	"regexp"

	"github.com/containers/podman/v5/pkg/bindings/containers"
	"github.com/containers/podman/v5/pkg/specgen"
	"github.com/docker/docker/api/types/container"
)

// JobSpec is deliberately independent of service configuration. Image must be
// locally available; no pull or retry is performed. Values are never logged.
type JobSpec struct {
	Image   string
	Command []string
	Env     []string
}

// JobResult is unknown unless Known is true. A known zero exit is success.
type JobResult struct {
	Known    bool
	ExitCode int64
}

// JobEngine creates anonymous, non-service containers and operates only on full IDs.
type JobEngine interface {
	CreateJob(context.Context, JobSpec) (string, error)
	StartJob(context.Context, string) error
	WaitJob(context.Context, string) (JobResult, error)
	RemoveJob(context.Context, string) error
}

var errJob = errors.New("one-shot job operation failed")
var jobID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validJobID(id string) bool { return jobID.MatchString(id) }

func (d *DockerEngine) CreateJob(ctx context.Context, s JobSpec) (string, error) {
	r, e := d.cli.ContainerCreate(ctx, &container.Config{Image: s.Image, Cmd: s.Command, Env: s.Env}, &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}}, nil, nil, "")
	if e != nil || !validJobID(r.ID) {
		return "", errJob
	}
	return r.ID, nil
}
func (d *DockerEngine) StartJob(ctx context.Context, id string) error {
	if !validJobID(id) {
		return errJob
	}
	if d.cli.ContainerStart(ctx, id, container.StartOptions{}) != nil {
		return errJob
	}
	return nil
}
func (d *DockerEngine) WaitJob(ctx context.Context, id string) (JobResult, error) {
	if !validJobID(id) {
		return JobResult{}, errJob
	}
	result, errs := d.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case r, ok := <-result:
		if !ok || r.Error != nil {
			return JobResult{}, errJob
		}
		return JobResult{true, r.StatusCode}, nil
	case <-errs:
		return JobResult{}, errJob
	case <-ctx.Done():
		return JobResult{}, errJob
	}
}
func (d *DockerEngine) RemoveJob(ctx context.Context, id string) error {
	if !validJobID(id) {
		return errJob
	}
	if d.cli.ContainerRemove(ctx, id, container.RemoveOptions{}) != nil {
		return errJob
	}
	return nil
}

func (p *PodmanEngine) CreateJob(ctx context.Context, s JobSpec) (string, error) {
	spec := specgen.NewSpecGenerator(s.Image, false)
	spec.Command = s.Command
	spec.Env = map[string]string{}
	for _, v := range s.Env {
		for i := 0; i < len(v); i++ {
			if v[i] == '=' {
				spec.Env[v[:i]] = v[i+1:]
				break
			}
		}
	}
	r, e := containers.CreateWithSpec(p.operationContext(ctx), spec, nil)
	if e != nil || !validJobID(r.ID) {
		return "", errJob
	}
	return r.ID, nil
}
func (p *PodmanEngine) StartJob(ctx context.Context, id string) error {
	if !validJobID(id) {
		return errJob
	}
	if containers.Start(p.operationContext(ctx), id, nil) != nil {
		return errJob
	}
	return nil
}
func (p *PodmanEngine) WaitJob(ctx context.Context, id string) (JobResult, error) {
	if !validJobID(id) {
		return JobResult{}, errJob
	}
	code, e := containers.Wait(p.operationContext(ctx), id, nil)
	if e != nil {
		return JobResult{}, errJob
	}
	return JobResult{true, int64(code)}, nil
}
func (p *PodmanEngine) RemoveJob(ctx context.Context, id string) error {
	if !validJobID(id) {
		return errJob
	}
	reports, e := containers.Remove(p.operationContext(ctx), id, nil)
	if e != nil || len(reports) != 1 || reports[0] == nil || reports[0].Err != nil {
		return errJob
	}
	return nil
}
