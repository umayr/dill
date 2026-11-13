# Bounded Alpha lifecycle guardrails

This is an opt-in **single-host, isolated, disposable-data pilot**, not a policy
for production workloads. Use only a dedicated disposable Docker target with
synthetic credentials. Portainer, hl and existing workloads remain authoritative
and must not be touched.

## No-prune up

```sh
dill --engine docker --engine-socket /absolute/disposable.sock --stack-name alpha-fixture \
  up --no-prune
```

Full-stack `up --no-prune` retains live services omitted from the desired config.
Desired services still receive normal ownership checks and reconciliation.
Partial `up service` retains its existing no-orphan-removal semantics. Ordinary
full-stack `up` retains its legacy orphan-removal behavior. This option does not
prevent recreation of a declared service.

## Disposable-volume-only profile

```sh
dill --engine docker --engine-socket /absolute/disposable.sock --stack-name alpha-fixture \
  up --alpha-disposable
```

Selected services must have effective `pullPolicy = "never"` (set it in the
config or pass `up --pull never`), and declared images must already be present
on the disposable target. Pulls are unsupported, including an `--pull always`
override. This is a prepared-image pilot.

Read-only local image metadata must include an immutable image ID and Config.
Every image-declared `VOLUME` is currently unsupported, even if a named mount
might cover that destination; missing metadata is not treated as empty storage.
Adapters inspect again before creation and pin create to the checked image ID
so retagging cannot substitute unqualified image storage after that check.

The profile implies no-prune even with `--no-prune=false`. Engine, local socket
and stack must be explicit; ambient Docker contexts and environment socket
selection alone are insufficient. Selecting a local socket is **not** proof of
daemon identity, isolation, or authorization; the operator must supply genuinely
disposable infrastructure. The profile does not provision or authorize it.

Before pulls or writes the profile rejects:

- All bind paths, including missing/inaccessible paths. Approved isolated-path
  qualification is **not implemented**; these mounts are unsupported, not
  classified as intrinsically invalid.
- External, anonymous, tmpfs and explicit-engine-name volumes. Only ordinary
  stack-scoped named volumes are supported.
- Host networking or any explicit network mode (including namespace sharing).
- Device mappings and added Linux capabilities.
- Secret declarations/grants and secret binding/policy mode. Existing qualified
  file-secret workflows remain available outside this narrower profile;
  production secret bindings are prohibited here.

Read-only adapter evidence must identify each network and declared volume as
absent or owned by this stack. Existing networks must be bridge networks;
existing volumes must use the local driver without options. Foreign ownership,
unsupported driver options (including NFS/bind-backed local volumes), missing
evidence and inspection failures stop the request before mutations. The same
storage checks and mutation-time adapter enforcement exist for Docker and
Podman; the **CLI Alpha target remains explicit local Docker only**.

The engine decorator rechecks evidence before network/container creation, and
adapters check volumes again when ensuring them, including create responses.
Decorators must explicitly forward `EnforceDisposableVolumes() error`,
`PreviewResources`, `DisposableImage`, `CLIEndpoint` and engine identity; unavailable enforcement
fails closed. Enforcement must be configured before concurrent engine use.

This is not NAS, mount identity, driver security, production data classification
or protection from a malicious concurrent daemon administrator. Volume APIs
identify volumes by mutable names, so inspection cannot make later mount
attachment atomic with ownership checks. External mutation/name reuse in that
window remains a qualification risk; do not claim cross-process coordination
without the separate mutation coordinator.

The attended operation adapter accepts the same up-only flags without changing
its result schema:

```sh
dill operation up --contract dill.operation/1 --engine docker \\
  --engine-socket /absolute/disposable.sock -p alpha-fixture \\
  --alpha-disposable --pull never
```

## Destructive intent and retained data

`rm` and `teardown` retain their prompts and `--yes` behavior. Container names,
service ownership and immutable IDs are captured **before** confirmation.
Prompts identify captured containers (and teardown image IDs); a confirmation
does not authorize later same-name replacement resources. Ownership and identity
are revalidated immediately before mutations, which target the captured
container ID rather than the name. Teardown additionally retains a retagged
image rather than deleting a new image selected after confirmation.

Machine-operation receipts preserve captured resource intents even on dry-run
or declined confirmation, using existing kind/identityDigest/action/effect fields.
Handler captures replace provisional census entries before any mutation. Actions
are prefixed `intent.` and have effect `none`; rm reports
`intent.removeStoppedCandidate` until it has checked stopped state, while retained
networks/volumes use `intent.retain`. These are intentions, not performed actions
or persisted authorization. The fixed machine prompt shows only the resource
intent count and aggregate digest; incomplete inventories fail confirmation,
including `--yes`. No second stdout JSON document or raw resource ID is emitted.

`down` retains its legacy non-prompting contract. Full-stack down captures all
identities before writes, keeps best-effort cleanup of independently owned
resources, and reports ownership/stop/remove failures. Orphan cleanup also pins
IDs before pulls and revalidates before stop/remove. A failed stop does not
silently erase the result of the following removal attempt.

Named volumes and existing networks remain conservatively retained by both
adapters; neither `--yes` nor `--force` grants permission to delete them.
Image deletion in legacy teardown retains its existing shared-image/force
semantics; this is not ownership proof for image content.

## Cleanup is not rollback

Failure/cancellation cleanup targets only immutable container IDs created by
that deployment. It may fail or leave retained resources, and partial failures
must be reported. It does not restore replaced containers, previous images,
persistent data, or a previous stack state. No automatic rollback is claimed.

## Evidence and remaining acceptance

Unit/HTTP-adapter tests cover no-prune, desired foreign ownership, unsupported
mounts/capabilities, fixed preflight errors, volume ownership/create-response
checks, decorator enforcement, confirmation-time name reuse, retained images,
cancellation and partial cleanup on both adapters. These are not real-engine
runtime parity evidence. Actual disposable engine qualification and combined
operation/coordinator qualification are separate gates. Approved isolated bind
paths and NAS identity validation remain outside this implementation; #79 must
not be declared fully complete on the basis of this bounded foundation.
