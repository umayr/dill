# Compose importer compatibility gaps

The importer is deliberately fail-closed; it does not currently translate
`depends_on` or named volumes, even where a startup or mount field looks
similar. It does not inspect images, engines, or the target environment. See the
[Compose specification](https://compose-spec.github.io/compose-spec/spec.html)
for the source semantics. No unsupported field is silently discarded.

## Dependencies

Compose short-form `depends_on` means `service_started` and requires dependent
services to be removed before their dependencies; long form also supports
health, successful completion, optional dependencies, and explicit restart
propagation. Dill's `dependsOn` orders startup and waits for its readiness
check (`internal/dag/dag.go`), but `down` obtains stack containers and removes
them without the declared dependency graph (`cmd/dill/down.go`,
`internal/dag/dag.go`). Therefore even short syntax is not a complete lifecycle
mapping. Enabling it requires dependency-aware reverse removal across the
relevant Dill operations and tests for both engines, plus an explicit contract
for readiness, failures, partial selection, and restart propagation. Until
then, remove and manage dependencies explicitly only after reviewing the
operational consequences; do not assume a generated equivalent exists.

## Named volumes

Compose top-level volume definitions carry project-scoped names, external
existence requirements and potentially driver options, labels, and explicit
names. Mount options include copy-up behavior. Dill has its own stack naming,
ownership and retention rules (`dill.pkl`, `internal/orchestrator/docker.go`,
`internal/orchestrator/podman.go`); names and teardown behavior are not proven
identical. Supporting a subset requires a documented mapping of project/stack
identity, external missing-volume errors, creation, copy-up, ownership,
retention/removal and engine parity, with tests against both engines. Until
then, migrate volumes manually under an explicit operator plan; never infer
that an existing Compose volume will be reused or deleted equivalently.

Builds, interpolation, aliases, unknown fields and other unsupported syntax
remain errors. Diagnostics intentionally omit input values; generated output
can contain secrets and should be protected accordingly.
