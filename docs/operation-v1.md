# Attended local operation results (`dill.operation/1`)

This is a bounded executable **CLI adapter**, separate from `dill protocol`.
The experimental metadata protocol remains unchanged and advertises only
`protocol.check`. Existing `validate --format operation-v1` still means the
narrow offline `dill.validate/1` format; it is not silently upgraded.

## Discover and select explicitly

```sh
dill operation-capabilities --format json
dill operation plan --contract dill.operation/1 --format json \
  --engine docker --engine-socket /absolute/disposable/docker.sock -p example -f compose.pkl
```

`operation` must be the first command token (after verbosity flags); its flags
follow the operation name, before any service selections. Contract selection is
mandatory; there is no version inference from `version`, `caps`, help text, or
the metadata protocol. Discovery is static adapter information, not target
qualification, authentication, or a readiness check. It lists exactly:
`validate`, `plan`, `diff`, `up`, `down`, `stop`, `start`, `restart`, `rm`, and
`teardown`, for explicit local Docker and Podman selectors. It does not list
`pull`, arbitrary subprocesses, providers, or remote execution.

Both engine and `--engine-socket` must be explicit, and a valid explicit stack
(`-p` / `--stack-name`) is required, even for validation. Ambient Docker contexts,
`DILL_ENGINE_SOCKET`, and engine defaults cannot select the target. Existing
local endpoint parsing/pinning is reused. A URI such as `unix:///path` and its
absolute path spelling produce the same endpoint digest, but symlink aliases
and different spellings are **not** proven to identify different/same daemons.

Invalid options, contracts, operations, targets, or service selections fail
before pulls/writes. Negotiation failures occur before configuration evaluation
or engine access. `--format json` is the only format. The adapter reuses current
Pkl evaluation and runtime/graph/name validators and current command handlers.
It does not accept an IR, cached external plan, or arbitrary JSON execution
payload. `validate --stdin`, env-file loading, and preview-proof publication are
not supported through this adapter. Existing legacy commands retain them.

The common action flags `--yes`/`-y`, `--quiet`, `--dry-run`, and `--timeout` are
accepted. `up` supports `--pull`, `--no-deps`, and `--force-recreate`;
`start`/`restart` support `--no-deps`; `teardown` supports `--force`.
`validate`, `plan`, `diff`, and `teardown` do not accept service selections.
Dry-run is not accepted for validation/plan/diff. Dry lifecycle operations use
existing handlers but suppress human output; dry `up` includes planned action
summaries, not raw configuration differences.

## One result, fixed diagnostics

A command emits one newline-terminated JSON document on stdout. Progress, pull
output, evaluator diagnostics, SDK diagnostics, raw errors, configuration,
image references, and plan before/after values are not emitted. Attended
`rm` and `teardown` retain their confirmation requirement unless `--yes` is
provided. The machine adapter uses an identity-free stderr prompt with a
resource-intent count and domain-separated digest of the full bounded intent
set. The count includes retention statements, not only destructive actions.
Incomplete intent inventories fail closed, even with `--yes`. This is not
saved-plan authorization. The operation-only prompt polls stdin with its
invocation context: deadline/SIGINT produces one cancelled result without
requiring EOF, closing the caller's descriptor, or abandoning a reader goroutine.
Legacy prompts, retention behavior, and legacy stdout are unchanged.

All results have these fields (the operation ID is freshly generated):

```json
{
  "version": "dill.operation/1",
  "operationId": "op_0123456789abcdef0123456789abcdef",
  "operation": "plan",
  "target": {"engine": "docker", "endpointDigest": "sha256:..."},
  "stack": "example",
  "outcome": "changes",
  "effects": "none",
  "changesDetected": true,
  "resources": [],
  "resourcesTruncated": false,
  "errors": []
}
```

Before safe argument validation, `target` is null and `stack` is empty;
unsupported operation input is never echoed (`operation` is empty). Supported
operation names may still be present on rejected requests. Errors are fixed
`{code,message}` pairs. No raw evaluator, parser, engine, or filesystem errors
are included. Failure to generate a secure ID or deliver stdout exits 1;
consumers must treat missing, malformed, or incomplete terminal output as
**unknown**, never success. There is no crash-recovery guarantee here.

| Outcome | Meaning / exit |
| --- | --- |
| `succeeded` | Handler completed successfully; exit 0. This is not application consistency or a transaction. |
| `changes` | Plan detected drift; `plan` exits 1, `diff` exits 0. Neither mutates the engine. |
| `invalid_input` | Unsupported contract/arguments/target or invalid decoded configuration; exit 2. Evaluator failure is not inferred to mean invalid configuration. |
| `failed` | Failed with no ambiguous mutation attempt; exit 1. |
| `cancelled` | Context cancellation/deadline (exit 1), or operator declined confirmation (exit 0, matching legacy behavior). Inspect `effects` independently. |
| `unknown` | An engine mutation attempt failed and its effects cannot be determined; exit 1. |

`effects` is independent of outcome:

- `none`: no observed engine mutation call was attempted (read-only operations,
  offline input rejection, declined prompt, or early preflight failure).
- `complete`: all observed mutation calls acknowledged success and the handler
  completed. This includes idempotent ensures and does not prove a change.
- `partial`: observed calls acknowledged success but a later non-mutation step
  failed/cancelled. No rollback is asserted.
- `unknown`: at least one mutation call failed or remained in flight. An error
  or cancellation does not prove the daemon did nothing, even with no returned
  container ID. Earlier successful calls can coexist with this value.

`changesDetected` reports computed desired-state actions, not a mutation audit.
`resources` contains bounded observations (maximum 4096 entries, with a truncation
flag), **not a complete recovery inventory**. Each entry has `kind`,
`identityDigest`, `action`, and `effect`. Destructive preflight candidates use
`intent.*` actions with effect `none`; they are distinct from performed calls.
Dry/declined `rm` uses `intent.removeStoppedCandidate` because running containers
may be skipped. Down/teardown containers use `intent.stopRemove`; teardown images
use `intent.removeCandidate` based on read-only `LocalImageID` capture, and
volumes/networks use `intent.retain`. These are candidates, not guarantees that
all actions will occur. Plan summaries use kind `service` with
`create`/`recreate`/`remove`/`noop` and effect `none`. Mutation summaries use
`container`, `image`, `network`, `volume`, or `createScope`.

`StartService` is a compound creation call: it may internally create networks,
volumes, and a container. `createScope` therefore represents the whole call,
not an assertion that only one container changed. An empty ID on failure still
produces unknown effects. Successful create scopes correlate to the returned
container ID; failed scopes retain the requested container-name digest. Network
and volume removal remain the adapters' read-only retention checks, not claimed
deletions. Errors in those checks still fail the handler.

`endpointDigest` is SHA-256 of the normalized explicit Unix URI and is only a
**selector digest**, not a canonical lock key or authenticated daemon identity.
Qualified mode additionally emits OPTIONAL `target.qualifiedIdentityDigest`
(`sha256:<64 lowercase hex>`) only after same-connection daemon qualification.
It is absent on unqualified and early-rejected requests. Discovery negotiates
this addition through feature `optInQualifiedLocalCoordination` and the optional
`qualifiedCoordinationOperations`, `qualifiedCoordinationEngines`, and
`qualifiedCoordinationLayouts` arrays. The identity digest is directly accepted
by offline `dill coordination inspect --target-id`; it does not change the
meaning of `endpointDigest`. See the [qualified coordination boundary](local-mutation-coordination-design.md).

Resource identity digests use SHA-256 of
`"dill.resource/1\u0000" + kind + "\u0000" + identity`. Container actions reuse
immutable IDs supplied by existing handlers when available. Action is a
separate field so stop/remove of one container correlate. Planned service
identity is `stack + "/" + service`. These are opaque correlation tokens, not
inspectable IDs, and not confidentiality proofs for low-entropy secrets.

## Secret boundary and safety limits

Secret-enabled operation mode is deliberately **unsupported**. Identifiable
secret selectors/policy reject before evaluation. Decoded secret grants reject
before engine access with fixed `SECRET_UNAVAILABLE` and no target/stack/resource
metadata. Evaluation is private from the start of this machine path, not only
after grants are discovered. Machine-private evaluation retains only closed,
fixed typed invalid-input/evaluator-failure/cancellation categories; raw parser,
filesystem, and subprocess errors are replaced, not wrapped. Existing secret
private evaluation still masks all failure categories exactly as before.
Do not use this path to bypass the existing Alpha
secret authorization, preflight, or lifecycle restrictions; legacy secret-aware
handlers remain independently supported and unchanged.

Unqualified use of this adapter alone does not implement qualified daemon
identity, cross-process coordination or a durable mutation journal/recovery.
Explicit qualified mode adds a bounded Docker-only implementation; it is NOT
universal protection for ordinary CLI/Brine/external-client workflows. The
separate disposable profile remains explicit; no atomic whole-operation
transaction or saved-plan authorization is claimed. Every advertised lifecycle operation first performs a full declared-container
and stack-listed container ownership census using the same invocation-owned
engine as its handler. A known foreign later target therefore rejects before
any earlier target can be mutated, including cleanup/image teardown. Operation
`stop` additionally captures immutable target IDs before its first stop. Existing
handler ownership revalidation and no-pull checks remain in place. External
changes after census may still produce partial or unknown effects; the census
is not an atomic engine transaction or an all-resources safety claim. This is
not a production deployment interface. #78/#79 integration and #80 real
engine qualification remain separate gates; #78 is not complete. Container/image operations remain
non-transactional. No real-engine parity is inferred from mocked unit tests.

## Consumer fixtures

[`internal/operation/testdata/manifest.json`](../internal/operation/testdata/manifest.json)
versions the shared success, evaluator failure, unsupported-contract, and
unsupported-operation fixtures. The fixture `runtime` names describe synthetic
test environments, not an execution payload. Consumers must validate the unique
operation ID shape before normalizing it for golden comparisons. Dill tests
compare complete result envelopes and exit codes. The separate manifest
`qualifiedFixtures` list adds `qualified-success.json`, exercised against the
real Docker SDK over a synthetic qualified Unix socket, not a live daemon.
These files are available for Brine consumers; no cross-repository consumer
qualification is claimed.

## Integration seam

`cmd/dill/operation.go` owns negotiation, the operation ID, private evaluation,
validated config caching, handler dispatch, and final output. The
`commandOptions.operation` state has a thread-safe `operation.Recorder` and the
already validated config. `loadConfig` only reuses this config for this adapter.
`newEngine` decorates and retains the pinned factory result with
`operation.Engine` for the full invocation; subsequent handler engine opens
borrow that same decorator. Handler closes do not release a borrowed engine;
the invocation releases it exactly once before terminal output. The decorator
preserves explicit adapter identity, CLI endpoint, resource-preview access,
and explicit disposable-volume enforcement forwarding (missing support fails
closed).
The decorator records attempts **before** delegating to the engine and never
changes returned errors. `Recorder.Finish` snapshots bounded resources and
conservative aggregate effects before stdout emission. `Recorder.Intent(kind,
identity, action)` adds an opaque candidate/retention entry. A handler can
atomically supersede provisional candidates using
`ReplaceIntents([]operation.Intent{...}) error`, with `Kind`, `Identity`, and
`Action` fields. Replacement is rejected after the first mutation attempt.
`ReplaceIntents(nil)` clears provisional intent. `IntentSummary()` returns
`(count, digest, complete)`; the digest sorts all captured tokens, domain-separated
with `dill.intent/1`, independent of capture order. More than 4096 intent entries
explicitly reports incomplete rather than presenting a bounded prefix as full
approval. Final wire resources still carry their existing truncation flag.

Explicit opt-in coordination qualifies and retains the same connected engine
for this entire handler scope, begins durable intent before any delegate
mutation, uses the same operation ID (including failed-start records), and
finishes durably **before** emitting the final result. A durable finish failure must not yield a succeeded receipt. SDK/transport
close is classified while locks remain held BEFORE durable finish. Post-commit
local lock/directory cleanup failure preserves the recorded engine outcome and
effects, emits fixed `COORDINATION_RELEASE_FAILED`, and returns exit 1; it does
not rewrite stdout-only unknown axes after recording success. The selector
digest cannot stand in for daemon attestation. Guardrails should forward explicit
supported flags to existing handlers rather than introduce a second lifecycle
implementation.
