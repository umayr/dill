# Explicit local engine endpoint selection

This is a narrow CLI foundation for [Dill #47](https://github.com/umayr/dill/issues/47)
and [Brine #5](https://github.com/umayr/brine/issues/5), **not trusted contexts or a
managed machine-operation API**. It does not complete #47, #19 or #42.

## Invocation and precedence

Select the engine type independently using the existing `--engine docker|podman`
override or `config.engine`. Select a local endpoint with:

```sh
dill --engine docker --engine-socket unix:///run/example/docker.sock -f compose.pkl ps
dill --engine docker --engine-socket unix:///run/example/docker.sock -f compose.pkl up
dill --engine docker --engine-socket unix:///run/example/docker.sock -f compose.pkl logs web
dill --engine docker --engine-socket unix:///run/example/docker.sock -f compose.pkl exec web true
```

These paths are examples, not discovery or authorization to contact an endpoint.
An embedding caller must pass its selected endpoint for **every** engine operation,
including inspection, lifecycle, logs, exec, top and cp. Label-only down/stop/rm,
volumes, stacks and doctor's config-failure fallback use the same selection path.
Dill does not compare its endpoint with another application's separately configured
engine client: that caller must propagate the same engine and endpoint.

Endpoint precedence, highest first:

1. `--engine-socket` (before the command, or after the command and before operands).
   A command-local occurrence overrides a global occurrence; repeated occurrences
   in the same flag set use the last value.
2. `DILL_ENGINE_SOCKET`, if present in the process environment.
3. Existing adapter selection, unchanged: Docker SDK environment/default selection
   (`DOCKER_HOST` and its existing TLS/API-version configuration); Podman
   `PODMAN_SOCKET`, otherwise the platform's existing socket default.

There is no endpoint field in `compose.pkl`, no implicit operator-config search,
and no named trusted-context file in this slice. Engine-type precedence is still
`--engine` over `config.engine` over the legacy Podman default; this change adds no
`DILL_ENGINE` override. The endpoint does not infer or override engine type.

Both `/absolute/path` and `unix:///absolute/path` select the same endpoint string
`unix:///absolute/path`. Empty explicit flags or a present-but-empty environment
override are **errors**, not requests to use an ambient fallback. Relative paths,
other schemes (including TCP/SSH), URI authorities/userinfo, escapes, query strings,
fragments, backslashes, control characters and invalid UTF-8 are rejected with a
fixed, input-independent usage error before constructing an engine. A connection
failure at the selected socket never falls back to another socket. This parser
checks syntax, not socket existence, host filesystem evidence or daemon identity.
It does not clean paths or resolve symlinks and must not be used as a lock key.

The existing explicit `--env-file` option loads missing process environment values
before engine selection; it can therefore supply `DILL_ENGINE_SOCKET` if not already
set. This preserves its legacy behavior, not a project-vs-operator trust boundary.
Treat env files and the subprocess environment as operator-controlled inputs; use
an explicit socket flag and a deliberate environment when embedding Dill. No new
host-environment injection into Pkl is added, and no provider executable or safety
policy can be configured by this endpoint option.

Offline/config-only commands do not construct an engine and do not inspect or
validate a selected endpoint (for example `images`, `config`, `validate`). `caps`
remains static adapter information, not proof that a target supports a feature.

## SDK and engine subprocess behavior

For an explicit local endpoint, Docker is constructed without `client.FromEnv`:
ambient host, TLS certificates and API-version overrides cannot redirect or
prevent local endpoint selection. The existing Podman constructor receives the
explicit endpoint rather than choosing an ambient URI.

Exec/top/cp retain their existing ownership checks and immutable container-ID
binding. Their engine subprocess receives the adapter's exact `--host` (Docker)
or `--url` (Podman) before the engine subcommand. For **local Unix adapter
endpoints**, subprocess environment excludes `DOCKER_HOST`, `DOCKER_CONTEXT`,
`DOCKER_TLS`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, `DOCKER_API_VERSION`,
`PODMAN_SOCKET`, `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY`.
Other environment, including registry configuration, is preserved. Legacy remote
adapter endpoints retain their previous subprocess environment/TLS behavior.
Logs use the already selected SDK client. This is endpoint pinning, not a promise
that all existing lifecycle/data-plane commands have future ownership, policy,
cancellation or structured-outcome semantics.

Do not put credentials in endpoint values. Invalid-override diagnostics do not
echo input. Legacy errors/output are not a general secret-safe protocol: arbitrary
backend errors and application log/exec output still have legacy behavior.

## Protocol compatibility and outstanding safety gates

The existing experimental protocol is unchanged: bootstrap `dill.bootstrap/1`,
wire `dill.dev/1`, only `protocol.check`, no IR/target/provider capabilities.
`--engine-socket` is **not accepted by `dill protocol` or `protocol run`**. Protocol
dispatch still occurs before legacy flags/environment/config/engine setup; invalid
protocol invocations do not echo the socket value. Discovery/check do not inspect
the endpoint, authenticate a target, or authorize a mutation. See the
[protocol contract](design/protocol-contract-19-proposal.md) and shared
[`internal/protocol/testdata`](../internal/protocol/testdata) compatibility fixtures.
Their partial/cancelled/unknown/change streams are codec fixtures, not implemented
runtime operations. The supported production protocol transport remains connected
Unix stream sockets on Linux only; ordinary pipes and macOS remain unsupported.

Legacy `plan` still exits nonzero for changes; `diff` exits zero for drift unless
it fails. Their unversioned JSON and legacy lifecycle text are not promoted to the
machine protocol by endpoint pinning. No binary-version inference or human-log
parsing establishes compatibility with future machine operations.

**Not implemented / not advertised:** authenticated daemon identity, trusted named
contexts/host assertions/provider configuration (#47), target-side preflight and
apply revalidation (#40), operator-authoritative policy (#41), secret-reference IR
and versioned semantic hashes (#38/#16/#10), artifact validation/apply (#19),
canonical target/stack cross-process locks, durable operation lookup/final/UNKNOWN
records, recovery or resumability (#42). A Unix socket can proxy a remote daemon;
local syntax does not prove target locality, authenticate its peer or make local
filesystem checks authoritative. Socket permissions and launcher trust remain the
operator's responsibility. No remote-agent-versus-SSH decision is made here.

Brine must keep unsupported managed machine mutations disabled until those
contracts and runtime safety gates exist. Do not substitute a Brine/process-local
mutex for Dill locking. A disconnect or ambiguous mutation outcome is not permission
to retry automatically.

## Evidence

- `TestLocalEngineSocketSelection` and `TestLocalEngineSocketRejectsWithoutEchoOrFallback`:
  precedence, syntax, empty overrides and sentinel-redaction negatives.
- `TestEngineEntrypointsShareSocketSelection`: global/command-local precedence
  through every engine entrypoint, including label-only operations and doctor's
  fallback; invalid overrides never reach the engine factory.
- `TestExplicitSocketPinsSDKAndSubprocesses`: disposable Unix HTTP fakes for both
  adapters, conflicting default socket/context/TLS/API-version selectors, SDK ps,
  logs and stop, immutable IDs and exact argv/environment for exec/top/cp. The
  wrong-default fake receives zero requests.
- Missing-endpoint and cancellation tests cover no fallback and bounded caller
  cancellation while acquiring a selected endpoint.
- Existing protocol boundary/codec tests continue to assert strict compatibility,
  redaction, ordering and failure handling, with new socket-flag negative cases.

These are fake-backed contract tests, not real Docker/Podman conformance or Brine
integration tests. Cross-process contention/crash-recovery tests belong with the
unimplemented locking/durable-outcome slice, not this endpoint selection change.
