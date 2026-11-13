# Qualified local mutation coordination (bounded Alpha)

Issue #78 is **not complete**. This slice protects only the explicit opt-in
operation mode described here, subject to independent review and disposable-host
qualification. Ordinary Dill commands, unqualified `dill operation`, the existing
metadata-only protocol, external engine clients and non-native deployments are
NOT protected. Do not mix those mutation paths into a qualified CLI/Brine layout
or claim global serialization. There is no distributed locking guarantee.

## Supported invocation and trust boundary

The initial layout is native CLI and native Brine co-located on one disposable
Docker host, under one dedicated operator account, sharing one pre-existing
absolute mode-0700 operator-owned state directory. The filesystem's cross-process
flock, file/directory sync and atomic local link behavior must be independently
qualified. `--coordination-layout native-local-docker-v1` explicitly attests those
facts; a pathname, owner/mode check or layout string alone does not prove them.
Unqualified namespaces, containers, separate state mounts, remote transports,
Podman and ambiguous filesystem semantics are unsupported. No production use.

Use only prepared local images and disposable/synthetic data. An operator must
independently establish the expected Docker daemon ID during provisioning; this
assertion is not authorization. Do not derive the expected ID from the same
untrusted endpoint immediately before accepting it as evidence.

Example **shape**, not a command to run against an existing workload:

```sh
dill operation up --contract dill.operation/1 \
  --engine docker --engine-socket /absolute/disposable/docker.sock -p alpha-test \
  --coordination-state-dir /absolute/private/alpha-state \
  --coordination-layout native-local-docker-v1 \
  --coordination-daemon-id EXPECTED_DISPOSABLE_DAEMON_ID \
  --coordination-source cli --pull never --timeout 30s
```

Brine uses the exact same invocation/state/target/stack and selects
`--coordination-source brine`. The source is a closed caller assertion, not an
identity/authentication system. All coordination selector flags are required;
partial or explicitly empty flags fail rather than falling back to an unlocked
operation. State initialization/journal writes are local metadata effects,
separate from the receipt's engine `effects` axis.

Qualified mutation operations: `up`, `down`, `stop`, `start`, `restart`, `rm`.
The normal destructive prompt remains in place for `rm`; `--yes` is explicit.
`teardown` is rejected before engine access because shared image deletion is not
qualified. There is no qualified `pull`, `exec`, `cp` or generic RPC operation.
Read-only validate/plan/diff can use their existing operation contracts, but do
not accept coordination flags or acquire a mutation session in this slice.

Additional conservative restrictions:

- `up` requires effective `pullPolicy = "never"` for every service, or an explicit
  `--pull never`. The guard refuses every attempted pull even if a future handler
  accidentally reaches one. Local image metadata must be available before writes.
- Image-declared `VOLUME` destinations are all unsupported initially. Otherwise
  Docker can create anonymous storage not present in the declared inventory.
  The shared disposable image preflight/strict adapter pins the inspected local
  immutable image ID at create time rather than trusting a mutable tag.
- The canonical #79 disposable static boundary applies automatically, without
  requiring a separate profile flag: bind/external/anonymous/explicit-engine-name
  volumes, tmpfs, special network modes, devices and added capabilities reject
  before socket access. Declared stack-scoped named volumes and explicit
  container names are inventoried and target-wide locked. Whole-resource
  read-only network/volume/image ownership preflight runs under those locks
  BEFORE any handler write; shared strict guardrails revalidate before create.
  The full inventory budget (at most 256 resource identities) is checked before
  opening a target or creating state metadata.
  Explicit `--alpha-disposable` may compose with this automatic qualified guard.
  This does not replace destructive intent enforcement or actual host qualification.
- The whole declared inventory is locked even for a subset operation. Live
  ownership/mount/network census runs while holding those same locks, before
  every handler. Actual attached-network metadata must be known and contain
  exactly the declared stack network; Docker default `bridge`/`default` host
  mode aliases are not themselves evidence of which networks are attached. Undeclared containers/orphans and uninventoried live storage
  fail closed, never get pruned. Existing ownership and immutable-ID checks in
  handlers still apply; a stale preflight is not a deletion authorization.

## Target and lock identity

`internal/coordination.Qualify` resolves/checks an existing Unix socket, opens ONE
connection and reads Docker `/info` on it. The returned client is injected into
the Docker SDK and retained for the entire operation, including ping/API version
negotiation, preflight, handler execution and cleanup. It imports no ambient
Docker context, proxy, host, TLS or API-version selection. Redirects and EVERY
second dial are forbidden. Socket replacement cannot retarget that connection;
connection loss fails closed rather than transparently reopening by path.
Streaming/hijack use, idle daemon disconnects and concurrency can reduce
availability. There is deliberately no automatic reconnect/replay.

The lock domain uses the actual qualified daemon ID fingerprint and exact
resolved engine stack label, not config filename, requested socket spelling or
lowercased names. Symlink/dot-path aliases and different qualified sockets for
the same daemon converge. A daemon clone/proxy that falsely reuses the qualified
identity is outside the independently provisioned uniqueness assumption.

The operation result's existing `target.endpointDigest` remains only a requested
selector digest, NOT canonical daemon identity. After successful same-connection
qualification only, optional `target.qualifiedIdentityDigest` is emitted as
`sha256:` plus the journal's actual-daemon target fingerprint. It is absent on
unqualified/early-rejected paths; consumers negotiate this additive field using
`optInQualifiedLocalCoordination` discovery. Discovery additionally lists the
qualified operations, engines (`docker`) and layout (`native-local-docker-v1`). The same random `op_<32 hex>` operation ID appears in
journal and result once a session exists. Requests rejected before acquiring a
session retain their independent receipt ID and no mutation journal entry.

Stack and resource keys are domain-separated keyed fingerprints derived using a
private persisted random HMAC key. Each container/network/volume reference has a
target-wide lock, allowing distinct stacks to proceed independently except when
they explicitly share resources. The library also supports image-pull references,
but qualified CLI mode deliberately prohibits pulling until image aliases/global
mutation semantics receive separate qualification.

All locks share one sorted order. Lock files remain persistent (never unlinked),
are regular single-link operator-owned mode-0600 files, and use kernel advisory
flock. Acquisition requires a deadline and honors cancellation. Filesystem
calls themselves are not deadline-bounded: the qualified local filesystem
assumption matters. PID ages/timeouts never authorize stealing an active lock.

## Durable reservations and terminal results

See `internal/coordination/README.md` for library APIs and exact invariants.

The canonical bounded `dill.coordination/1` record contains:
`version`, `operationId`, closed `source`, actual `target`, keyed `stack`,
`resources[{kind,id,action}]`, `state`, `outcome`, `effects`. It never contains
config/environment/credentials/paths/arbitrary diagnostics/raw engine responses.
Fingerprints are private correlatable metadata, not public anonymization.

`Begin` holds the full stack/resource lock set, checks ALL prior reservation
heads (including those from different stacks), writes a durable in-progress
stack head first, then every durable resource reservation. Only after all
file+directory syncs succeed may any engine mutation occur. Interrupted installs
can conservatively block operations even if no effects were started.

Heads are never replaced with success during completion. `Finish` first saves an
independently durable immutable `<operationId>.result`. Only after its file AND
directory syncs succeed does a final atomic local link publish a `.commit` alias.
No fallible write/checkpoint/sync follows that last link. Readers require the
exact two-link result/commit inode pair and matching head metadata; other files
continue to reject hard links. Persistent terminal sync failure leaves the
original head unresolved even if terminal bytes happen to be visible. A failed
`Finish` becomes an unknown receipt, not success, and blocks later mutations.

The publication link itself is intentionally not separately dir-synced. If lost
after crash, independently durable result bytes remain but recovery treats the
operation as unknown: conservative inspection, never invented success. This
protocol requires qualified LOCAL link syscalls whose failure creates no link;
filesystems with ambiguous outcomes are unsupported. There is no best-effort
rollback of a visible terminal head pretending to establish durability.

Malformed, truncated, unsupported, mismatched or unsafe-inode records fail
closed. A missing identity key in any populated/ambiguous state directory is
corruption, not permission to re-key: only a proven fresh directory containing
the known bootstrap lock can initialize a key. Existing unknown records cannot
become invisible by accidental key loss.

## Offline inspection and explicit acknowledgement

Recovery has no Pkl/config evaluation, engine, secret source or socket access:

```sh
dill coordination inspect --state-dir /absolute/private/alpha-state \
  --daemon-id EXPECTED_DISPOSABLE_DAEMON_ID --stack alpha-test --timeout 5s

dill coordination acknowledge --state-dir /absolute/private/alpha-state \
  --daemon-id EXPECTED_DISPOSABLE_DAEMON_ID --stack alpha-test \
  --operation-id op_EXACT_INSPECTED_ID --inspected --timeout 5s
```

`--daemon-id` derives the SAME fingerprint offline; alternatively supply the
emitted `target.qualifiedIdentityDigest` directly with `--target-id` (never both).
Both `sha256:<64 hex>` and the raw recorded hex token are accepted. The optional
attested field does not repurpose the requested endpoint selector digest. For cross-stack resource reservations,
replace `--stack` with `--resource-kind container --resource-name EXACT_NAME`;
volume/network/image namespaces are likewise bounded by the library inventory.

Inspect the actual disposable target/effects independently BEFORE passing
`--inspected`. In-progress, failed, cancelled, partial and unknown outcomes never
imply rollback or no effects. An acknowledgement takes the exact operation ID,
locks its full RECORDED inventory in the same order and revalidates under locks.
It publishes a separate durable acknowledgement result/commit pair, retaining
original evidence and coherently resolving only that one operation. Active,
changed, corrupt or wrong-ID state cannot be cleared. There is no blind replay,
force unlock, delete-record, automatic retry or repair command.

Records/results/acknowledgements remain indefinitely; automatic retention GC is
not implemented. Orphan `.pending-*` files are not replayed. Never delete/replace
state, key or lock files while ANY caller runs. Corrupt state requires stopping
all callers and forensic repair, not acknowledging guesses about engine effects.

## Evidence and remaining gates

Synthetic tests cover actual Docker SDK use over the qualified Unix transport,
all six supported handlers, aliases/socket replacement, redirects/proxy/reconnect
refusal, parallel reads, CLI/Brine process contention, deadline/cancellation,
process death, interrupted installs/writes, persistent terminal write failures,
shared-resource crash blockers, key loss, inode/link checks and offline recovery
routing (including stack label `protocol`). Mocks are NOT live engine evidence.

Still required: independent final-head review, combined #77/#79 verification,
opt-in disposable Docker engine and actual native Brine qualification, host/state
filesystem evidence, and bypass-path exclusion/enforcement for the chosen
operator workflow. Containerized Brine, Podman-qualified coordination, global
image deletion, distributed callers and global legacy CLI safety are NOT claimed.
