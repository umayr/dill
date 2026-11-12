# Local mutation coordination: draft foundations

This is a design note, not an implemented safety guarantee. `internal/stacklock`
is not wired into any command or protocol. Dill currently does **not** serialize
mutations or persist crash outcomes. Issue #78 remains blocked on #77.

## Proposed identity and qualification

The logical scope is a versioned, unambiguously encoded pair of the **actual
local engine target** and the exact resolved stack label used by the engine,
not the config filename or raw command-line arguments. The CLI already resolves
stack overrides before config names and path-derived names. Thus two config
paths yielding the same label must contend, while distinct labels on one target
can proceed independently. The identity must be established after the actual
target and label are resolved, and before any engine mutation, including pulls
or orphan removal. Do not lowercase or otherwise transform engine labels.

For the first qualified layout, both callers must run on the same host and
operator account with one configured, trusted local state directory and an
explicit local Docker socket. Candidate normalization includes resolving dot
components and symlinks of the existing socket path, with socket type checks;
using path spelling alone is insufficient. This does not prove two different
bind mounts, hard links, proxies, or separate sockets lead to different daemons.
A qualification procedure must resolve those cases or reject them; canonical
paths alone are not a proof of target identity. The endpoint used for engine
operations must remain bound to the qualified identity throughout the operation;
replacement/retargeting races need an explicit solution. Unsupported engines,
remote transports, ambiguous aliases and unqualified namespace/container
layouts must fail before mutation, not silently fall back to an unlocked path.
The lock directory must be on a filesystem whose cross-process flock semantics
and crash durability have been qualified, with permissions and ownership checked.
All entry points, including independently launched CLI and Brine operations,
must cooperate; a lock cannot protect against external engine clients.

A per-stack lock alone cannot serialize operations on shared global engine
resources (for example explicit container names or shared resources across
stacks). Before integration, audit each mutation for cross-stack conflicts and
establish additional coordination or reject unsupported cases. Define lock order
before introducing multiple locks.

## Lock primitive

The new primitive accepts an opaque 32-byte key and a trusted, existing absolute
directory; it deliberately specifies neither identity encoding nor directory
selection. It requires a context deadline, polls a nonblocking OS advisory lock,
and never unlinks the file. Keeping the inode prevents a waiter from locking an
obsolete file after a release. Kernel locks normally release when the last
reference to the locked open file description closes; a forked child retaining a descriptor can prolong a lock
after the original process dies (the descriptor is close-on-exec). This does
not update an operation record. Deadlines bound contention polling, not open,
stat, flock, close, or other filesystem calls, which can block; a qualified
local filesystem remains essential. The primitive rejects symlinks only at the
final path component and does not prevent hard links or replacement. Its
contract assumes no untrusted directory writers or lock file replacement; it does not establish those assumptions. Do not expose it as
an operational guarantee without qualification and integration.

## Proposed crash-outcome persistence

While holding the lock and before the first possible mutation, durably create a
versioned operation record with a unique operation ID and a redacted, bounded
source identity, canonical target identity, resolved stack, and known affected
resource identities. An in-progress record found after acquiring the lock is
unknown/partial, never evidence that no effects happened and never a replay
instruction. Record a terminal outcome only when supported by the operation's
actual result; cancellation and failures may still have partial effects.
Unknown state must be surfaced for operator inspection before subsequent
mutation, with explicit resolution semantics rather than automatic deletion.

Persistence needs a restrictive trusted directory, schema/version validation,
atomic replacement via a same-directory temporary file, file sync and directory
sync at defined commit points, and recovery tests for each interruption window.
Malformed, truncated, unsupported, or ambiguous records must fail closed. Do not
store configuration, environment, credentials, arbitrary diagnostics, or raw
engine responses. Define limits and redaction for resource/source identifiers;
a hash is not automatically non-sensitive. Handling a failed durable write
before mutation differs from a failed terminal write after effects: the latter
must remain unknown, not success. Record retention, explicit acknowledgement,
and how to associate later discovered resources remain open.

## Integration gates

Agree with #77 on operation boundaries, target and stack identifiers, resource
identities, terminal and unknown/partial semantics, and the protocol representation.
Then cover all mutation entry points end-to-end with the same lock and journal,
including CLI/Brine concurrent processes, timeouts and cancellation, alias and
unsupported-layout tests, injected filesystem failures, killed processes, stale
and corrupt records, and engine-backed tests on a disposable target. No partial
wiring: one unprotected mutation path invalidates the coordination guarantee.
