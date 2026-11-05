# AGENTS.md

Instructions for AI coding agents working in this repository.

## What this is

Dill is a strictly-typed, configuration-as-code container orchestrator built with
Go and Apple's Pkl configuration language — a modern, type-safe alternative to
Docker Compose with native Podman and Docker support. Configuration is written in
Pkl (`compose.pkl`), with Pkl type checks and Go runtime validation. Services
start in dependency (DAG) order. Complete pre-mutation validation is still being
hardened (#29, #32).

## Build, test, validate

```sh
make build          # bin/dill (build tags handled for you)
make test           # unit tests, all packages
make integration-test            # needs a running engine (Docker or Podman)
DILL_TEST_RUN=TestX make integration-test   # single test
make vet            # go vet with correct tags
make fmt            # gofmt + pkl format
make validate       # package schema + eval examples/*/compose.pkl
```

**Critical: always use `make` targets (or include the tags).** Go builds require
`-tags containers_image_openpgp,exclude_graphdriver_devicemapper,exclude_graphdriver_btrfs`.
A plain `go build`/`go test` fails or misleads. Integration tests additionally
need the `integration` tag; prefer `make integration-test`.

Toolchain: `go.mod` declares the minimum Go version (currently 1.25.0).
The Pkl download version is pinned in `internal/loader/pkl.go` (currently 0.31.1);
integration CI uses that version, while other Pkl jobs currently use `latest`.
If `pkl` is not on `PATH`, Dill can download the pinned release but requires
checksum verification (`DILL_PKL_SHA256`); prefer `DILL_PKL_PATH` pointing at a
local binary.

## Architecture map

- `cmd/dill/` — CLI entrypoints. One file per command group (`up.go`, `down.go`,
  `plan.go`, `logs.go`, …). Wires flags → config → orchestrator.
- `internal/loader/` — locates Pkl and downloads it with checksum verification
  when needed.
- `internal/config/` — invokes Pkl, parses its JSON output, validates, normalizes,
  and hashes configuration. `security.go` protects reserved labels and checks
  port, mount, and healthcheck-test normalization; it does not enforce a policy
  against dangerous bind mounts or capabilities.
- `internal/dag/` — dependency resolution for `dependsOn`; topological start
  order, cycle detection.
- `internal/orchestrator/` — engine abstraction. `orchestrator.go` defines the
  interface; `docker.go` and `podman.go` implement it via native Go SDKs.
- `internal/plan/` — desired (Pkl) vs live engine state diffing; `render.go`
  formats human/JSON output.
- `dill.pkl` — the schema: `Service`, `Port`, `Mount`, `HealthCheck`,
  `DillConfig`. The single source of truth for what users can declare.
- `examples/` — runnable sample configs (basic, loops, fullstack, overrides,
  replicated, remote, monitoring). Used by `make validate`.
- `test/integration/` — real-engine integration tests. `scripts/` has Colima
  (Docker) and Lima (Podman) helper scripts for macOS.

Deployment flow: `loader` (resolve Pkl) → `config` (evaluate, parse, validate) →
`plan` (diff vs live) → `dag` (dependency-ordered execution) → `orchestrator`
(engine operations). `up` checks ownership before pulling `always` images, then
recomputes the plan after those pulls. It still removes orphans before graph
validation; do not assume preflight is mutation-free until #29 is fixed.

## Conventions

- Engine parity is required for new changes: every feature added to `docker.go`
  must work identically in `podman.go` and vice versa. Existing gaps are tracked
  in #15 and #28. Add engine operations behind the `orchestrator` interface,
  not as engine-specific branches in `cmd/`.
- Schema changes start in `dill.pkl`, then add config normalization/validation,
  both engine implementations, config-hash updates, and plan/drift detection
  where applicable. Include an example and tests for normalization,
  reconciliation, and both engines in the same change.
- Pkl gotcha: Pkl's JSON renderer cannot serialize `Duration`. `dill.pkl`
  converts durations to `{value, unit}` — the Go side reconstructs
  `time.Duration` from that shape. Preserve this when adding duration fields.
- Config keys use camelCase (Pkl convention: `pullPolicy`, `dependsOn`,
  `containerName`). JSON keys match the Pkl property names. Compose import is
  planned (#7); any future importer must map Compose's snake_case keys.
- `plan` is CI-oriented: exits non-zero when changes exist. `diff` renders the
  same info but exits zero unless it fails. Don't blur these exit-code contracts.
- `rm` and `teardown` prompt before destructive actions; `--yes` skips the
  prompt. `down` currently does not prompt. Never remove an existing prompt to
  make a test pass.
- Deterministic naming: stacks without an explicit `name` derive a
  adjective-animal name from the config path. Don't introduce randomness.

## Worktrees and delivery

- All feature work must use a new branch in a new Git worktree under the
  repository-root `.worktree/` directory, not the main checkout. For example:
  `git worktree add .worktree/my-feature -b feat/my-feature main`.
- `.worktree/` is ignored by Git. Keep each worktree to one writer and preserve
  unrelated changes; never reset or stash someone else's work to switch branches.
- Run checks from the worktree containing the change. Before pushing, review the
  full diff, run `git diff --check`, and run the applicable build, test, vet, and
  Pkl validation commands. Report any checks that could not run.
- Keep changes scoped: no unrelated refactors, generated clutter, speculative
  abstractions, or claims that are not supported by the code and checks.

## Testing

- Unit tests live next to the code (`*_test.go` in each package).
- Integration tests require a live engine and the `integration` build tag; on
  macOS use `scripts/integration-colima-docker.sh` or
  `scripts/integration-lima-podman.sh` to provision local VMs.
- Validate Pkl changes with `make validate`; it evaluates
  `examples/*/compose.pkl`, not every Pkl example. When changing the schema or
  overrides, also run
  `DB_USER=test DB_PASSWORD=test pkl eval examples/overrides/my-server.pkl`.
  The remote example imports a published schema, not the working-tree schema.

## Project memory

Persistent cross-session knowledge for this repo lives in the pi memory repo at
`~/.pi/memory-md/dill/` (architecture decisions, gotchas, testing setup). Check
it before large refactors; record durable decisions there rather than leaving
them only in conversation.

## Agent fleet

Local Pi configuration under `.pi/` is ignored and may not exist in a fresh
checkout. When present, `.pi/settings.json` configures subagent model routing;
keep it in sync with any local agents added under `.pi/agents/`. Delegate only
when the user or applicable instructions authorize it.
