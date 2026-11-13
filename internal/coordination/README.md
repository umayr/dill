# Local coordination library (not yet an operational guarantee)

The package is deliberately **not wired to the legacy CLI or protocol** in this
commit. No existing mutation is protected merely by importing this package.
Issue #78 remains open until explicit operation-mode integration, independent
review and disposable engine qualification pass.

## Qualified boundary

- Native CLI and Brine on one disposable Docker host, one dedicated operator
  account, one shared pre-existing absolute operator-owned mode-0700 directory.
- The operator must independently qualify this layout, filesystem flock/fsync/
  atomic local link semantics, account isolation, daemon identity uniqueness,
  and endpoint access. `native-local-docker-v1` is an explicit attestation, not
  automatic detection or authorization. Container/namespace/distributed/NFS
  layouts are unsupported; passing a false native attestation is not supported.
- Explicit existing local Unix socket and independently provisioned expected
  Docker `/info` ID. Remote targets, other engines and guessed identities fail.
- Trusted state must not be deleted, substituted, copied or independently
  provisioned per caller while callers run. Private descriptors pin state opens;
  they cannot prevent an operator from splitting callers across directories.
  External Docker clients do not cooperate and are outside this guarantee.

`Qualify` opens one connection, checks actual daemon ID on that connection, and
returns the ONLY client usable for the operation. Configure the Docker SDK with
that client and `Endpoint`; do not reconstruct a client from the original socket
or allow environment fallback. Redirects, proxy configuration and every second
dial are forbidden. Two sockets reporting the qualified same daemon ID converge;
socket aliases are not used as daemon identity. Socket replacement cannot move
an established operation to a different daemon. Disconnects, daemon idle closes,
streaming/hijack use and concurrent HTTP requests can reduce availability: there
is deliberately no transparent reconnect. `Target.Close` is idempotent.

## Integration API

```
target = Qualify(ctxWithDeadline, Qualification{Layout, Socket, DaemonID})
state = Open(ctxWithDeadline, existingPrivateDirectory)
session = state.Begin(ctxWithDeadline, target, exactResolvedStack,
                      "cli" or "brine", completeResourceInventory)
// only now may the already-qualified engine mutate
session.Finish(actualOutcome, actualEffects)
session.Close()
target.Close()
state.Close()
```

Every potential engine mutation, including adapter-internal pulls and resource
creation, must be covered by a complete inventory BEFORE `Begin` returns. Locks
for stack and resource fingerprints share a single total order. Container,
network, volume and image-pull references are supported; image deletion is not.
Inventory construction and engine usage/ownership checks belong in integration.
A declaration that a resource is stack-scoped is not enough to skip its lock.
Read-only preflight snapshots may need rebuilding after locks are acquired.

Do not publish the terminal operation result until `Finish` succeeds. On its
failure report unknown effects and require inspection, never success or replay.
The result operation ID is `session.OperationID()`. Unknown/partial/cancelled
outcomes can have effects; cancellation does not imply rollback.

## Durable protocol and recovery

The bounded canonical `dill.coordination/1` record contains `version`, random
`operationId`, closed `source` (`cli` or `brine`), actual-daemon fingerprint
`target`, keyed `stack`, `resources` (`kind`, keyed `id`, closed `action`), `state`,
`outcome`, and `effects`. No config, paths, environment, engine text or arbitrary
errors are stored. Stack/resource/lock tokens use a persisted private random
HMAC-SHA256 key with domain separation. These are correlatable private metadata,
not public anonymization or a license to hash secret values into records.

1. `Begin` locks the full sorted inventory, then reads every reservation (even
   reservations owned by other stacks). Any unresolved or malformed reservation
   fails closed. The stack head is durably written first, then each resource
   head. File sync and directory sync complete before mutation is allowed.
2. Heads remain `in_progress`; `Finish` NEVER replaces them with success.
   Terminal bytes are stored in a separate immutable `<id>.result`, atomically
   replaced from a same-directory temporary file, file-synced and dir-synced.
3. Only AFTER that succeeds does a final atomic `linkat` publish `<id>.commit`.
   No fallible checkpoint/write/sync follows the link. Readers require exactly
   the matching two-link inode pair, correct ownership/mode/type, canonical
   schema and the same operation metadata as the head. Other files still reject
   multiple links. Failed terminal sync cannot authorize mutation even if the
   result bytes are visible; the original head remains unknown.
4. The commit link is intentionally not separately directory-synced. If it is
   lost after crash, the independently durable result bytes remain retained but
   the operation is conservatively unknown. This costs availability, not safety.
   This protocol assumes a failed atomic local link syscall creates no link;
   filesystems with ambiguous link outcomes are NOT qualified.
5. `Inspect` and `InspectResource` acquire the appropriate lock and read without
   modifying state. Active operations cannot be mistaken for abandoned ones.
   `Acknowledge`/`AcknowledgeResource` require the exact inspected operation ID,
   acquire the entire RECORDED inventory in the same total order and revalidate.
   An acknowledgement publishes its own durable `<id>-ack.result` + commit pair,
   never replacing original terminal evidence or claiming no effects/rollback.
   It coherently resolves all reservations for that one operation. A wrong,
   stale, corrupt or still-active record cannot be cleared.

Results and acknowledgements are retained indefinitely in this first slice.
There is no automatic replay, cleanup, pruning, lock-file unlink, stale-PID
stealing or TTL. Orphan `.pending-*` files are not committed state and never
replayed. No repair command for malformed state is provided: stop ALL callers
and perform forensic inspection before any manual repair. Filesystem errors,
namespace/account uncertainty and unavailable durable state are fail-closed.

Synthetic tests exercise aliases/replacement, refused reconnects and redirects,
process death, deadlines/cancellation, independent stacks, explicit shared
resources, persistent write-failure windows, exact-ID acknowledgement and inode
validation. These do not qualify an actual Docker host or Brine deployment.
