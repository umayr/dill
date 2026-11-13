# Required combined Alpha runtime cases (wire bound; runtime pending)

This is an execution design for #80, **not executed evidence**. The supervisor's
approved #78 composition `86e9d07a2bebe515aeeec48729e29c5ecb0a4d8f`
now binds the wire fields below; #79 correction
`0283cb3d0f8ff7cc48e395a3f1efc1c0d0d8f04c` is incorporated too.
The executable consumer recognizes only the specific reviewed additions and
continues rejecting unknown fields. Operation IDs retain the `op_` shape.
Existing baseline engine parity and Alpha secret CI remain mandatory.

## Bound negotiated interface

- Discovery retains `version`, `operations`, `engines`, `features`. The reviewed
  feature is `optInQualifiedLocalCoordination`; its optional arrays are
  `qualifiedCoordinationOperations` (up/down/stop/start/restart/rm),
  `qualifiedCoordinationEngines` (`docker`) and `qualifiedCoordinationLayouts`
  (`native-local-docker-v1`). Final #78 is native **Linux-only**; other hosts
  must advertise no feature and **omit** the optional keys. Any key presence
  requires the feature plus all three complete supported lists. Empty/null,
  partial or unknown additions fail closed; producer bugs are not a fallback.
- `target.endpointDigest` remains the requested selector digest, **not daemon
  attestation**. Optional `target.qualifiedIdentityDigest` is `sha256:` plus the
  actual attested identity hex. It is absent for unqualified/early-rejected
  targets. The external decoder consumes the producer's actual
  `internal/operation/testdata/qualified-success.json` fixture; malformed, null,
  empty and non-prefixed identities and unknown fields remain rejected.
- Exact qualified flags: `--coordination-state-dir`, `--coordination-layout`,
  `--coordination-daemon-id`, **`--coordination-source cli|brine`**. No proposed
  `--operation-source` flag is used. These are a caller assertion, not proof a
  real Brine process consumed the candidate.
- Offline `dill coordination inspect|acknowledge` uses `--state-dir`, either
  `--daemon-id` or `--target-id`, `--stack` (or resource kind/name), and for
  acknowledgement exact `--operation-id` plus `--inspected`. `--target-id`
  accepts the emitted prefixed digest and normalizes it to raw hex. No engine,
  config evaluation, replay or automatic rollback is part of recovery.

## Fixture boundary

- One exclusively disposable native local Docker daemon, synthetic data and
  registry-free credentials/config, one fixed candidate SHA/binary/schema/Pkl and
  versioned fixture bundle. Do not reuse an operator desktop/production engine.
- Explicit `native-local-docker-v1` qualification, expected daemon ID, trusted
  state directory and `cli`/`brine` source using the exact bound flags above.
  Expected daemon identity must be independently established during disposable
  provisioning, not self-attested from an untrusted selected socket.
- Prepare local busybox/alpine images before qualified execution. Positive images
  have **no image-declared VOLUME entries**. Qualified config uses effective pull
  **never**; assert no pull occurs. Both adapters inspect and pin the immutable
  image ID used for creation, not a subsequently movable tag.
- All image-declared VOLUME entries are initially rejected, **including entries
  exactly covered by dedicated named mounts**. Unknown image metadata fails
  closed. Separate negative fixtures cover hidden anonymous storage, tag
  retargeting between inspect/create, and metadata failure. Do not infer safety
  from a matching declared mount alone.
- Only dedicated named stack volumes; no binds, secrets, capabilities/devices,
  host namespace, external/anonymous/explicit-name volumes, or uninventoried
  mounts/orphans. Qualified operations are up/down/stop/start/restart/rm;
  teardown is explicitly unsupported. Preserve retained-volume semantics/prompts.
- Capture a synthetic **foreign** running container, named volume and network
  inventory before each scenario. Compare immutable identities, ownership labels,
  running state, mounts and synthetic data markers after every attempt, recovery
  acknowledgement and scoped cleanup. Never upload arbitrary inspect output.
- Use process/journal/observed-readiness barriers with bounded deadlines. A sleep
  alone is not proof a writer acquired the lock or entered mutation. A test that
  never reaches its intended interruption window fails; it does not skip/pass.

## Mandatory scenario inventory

All rows are required. Their acceptance IDs are enforced by `final_acceptance`,
not optional capability flags. None is implemented by this document.

| Acceptance ID | Process setup / interruption | Required observation |
| --- | --- | --- |
| `qualified-docker-profile` | Execute each supported qualified operation with prepared images, exact target and trusted state. | Discovery/qualified identity is negotiated, IDs correlate result+journal, effective pull never, no unapproved storage, durable finish precedes terminal success. |
| `qualified-podman-rejected` | Request qualified Podman with the same explicit contract boundary. | Explicit unsupported rejection before pull/write; no fallback to unqualified handler, no changed Docker/Podman/foreign resources. This is not qualified Podman parity. |
| `qualified-image-volume-rejected` | Prepared image declares VOLUME, with/without a matching named mount; unknown metadata; retarget tag after inspection. | All VOLUME and unknown metadata cases reject before mutation; immutable inspected image ID prevents tag-retarget storage bypass. |
| `same-stack-concurrency` | Writer A reaches durable pending intent while writer B uses same daemon+stack, with source cli then brine and swapped order. | B waits/rejects according to final contract before any write; never two writers; B's eventual receipt reflects post-A live state. CLI and Brine contend on the same key. |
| `alias-concurrency` | Repeat same-stack contention using absolute socket path, Unix URI and symlink alias, with conflicting ambient selectors. | Alias spelling cannot create an independent lock; authenticated daemon+stack wins. Requested endpoint digest remains only a selector. No reconnect/proxy redirection accepted. |
| `different-stack-concurrency` | Hold A at a known post-lock barrier; execute B on a distinct synthetic stack on the same daemon. | B completes before A releases; no accidental global lock. Explicit shared-resource conflict, if requested, rejects without changing A/foreign resources. |
| `crash-unknown-recovery` | SIGKILL A after durable pending intent and after a proven engine effect but before durable finish. Also lose the fixed connection during mutation. | Kernel lock release is not success. Next same-stack mutation fails closed on unfinished/unknown record; missing terminal JSON is unknown. Different stack remains independent. No reconnect, retry/replay or inferred rollback. |
| `corrupt-journal-rejection` | Corrupt/truncate a copied synthetic journal; stale daemon identity/stack/layout/schema; unsafe state-directory permissions/layout. | Reject before pull/write; do not auto-delete/repair/reinterpret state. Disallowed filesystem layouts remain unsupported. |
| `inspected-exact-operation-recovery` | Read-only inspect of the unknown record and actual live resources, then wrong-ID ack, omitted-inspected ack, exact-ID inspected ack. | Wrong/absent intent cannot clear uncertainty. Exact inspected acknowledgement only clears its record; it does not execute/replay/rollback. A fresh operation gets a new ID and reconciles observed state. |
| `operation-outcomes` | Success, no-op, invalid config, deadline/SIGTERM, ambiguous mutation failure and durable-finish failure on combined handlers. | Exact fixed/redacted result; partial/unknown effects stay conservative; failed durable finish never emits succeeded; no raw config/source/engine error leakage. |
| `no-prune` | Isolated CLI `up --no-prune`, omit a running owned service, then successfully repeat with coordination/profile flags ABSENT. | Explicit positive preservation of omitted ID/running state on the pinned engine; approved `engine` / `real-engine` row. Qualified mode forbids orphans and separately must reject them zero-write, never counted as this positive result. |
| `bounded-storage-preflight` | Mount/capability/namespace/secret/image-policy violations; live resource census changes after prompt. | Bounded profile rejects before mutation; no orphan or uninventoried mount slips through; denied requests leave all snapshots unchanged. |
| `foreign-resource-inventory` | Check the dedicated inventory around **every** above scenario and cleanup, not only collision. | Foreign container/network/volume identity, state and data remain unchanged. Cleanup is not database rollback. |
| `brine-candidate-handoff` | Supply immutable exact binary+schema+versioned fixtures, verify copied checksums, exercise copied qualified CLI caller contract with source brine when checking usability. | Dill-side artifact supply and pinned caller contract only; no released-old-binary substitution. Actual Brine app/browser E2E is downstream #69, separately NOT RUN/unproven, not a prerequisite for #80 supply. |

## Evidence and completion gate

The current manifest leaves the final combined rows blocked. Narrower #79
`--profiles` runs populate only the separate `preparedProfiles` observations;
those results cannot satisfy any required row. Required positive no-prune is
instead explicitly bound to its actual isolated CLI test and ordinary engine
evidence, as approved; it is not automatically promoted from prepared output.
In particular, qualified
unsupported/foreign preflight must run **without** the separate
`--alpha-disposable` flag, so that flag cannot hide a coordinator preflight gap.
Once implemented, each combined row gets its own actual selected test and real
qualified-engine evidence, bound to this run's
candidate commit, binary digest and explicit target. The runner must supply the
approved qualified target/state interface rather than accepting a forged
`passed` fixture or optional evidence file. No manual waiver switches.

`--require-complete` makes incomplete final acceptance nonzero even when baseline
checks pass. The gate requires the exact required-case set (missing/duplicate/
extra IDs fail), every status passed, actual runtime provenance for each runtime
case, clean exact source/build identity, final integrity verification and stated
native-Linux Docker-only profile limits. Expected mode and evidence kind are
fixed **per required ID independently of the manifest**. Only `offline-valid`
and `offline-invalid-graph` may ever be offline; changing any runtime row to
`offline` (even with matching offline evidence) or an unknown mode fails. Qualified rows specifically require qualified-runtime
evidence; an ordinary engine test with a similar name cannot satisfy them.
Current blocked rows and unbound qualified modes cannot produce completion.

Unit tests exercise this predicate with synthetic input, including one complete
input to show it is not permanently hardcoded incomplete. Such unit input is
never published as runtime evidence. Final completion still requires independent
review, exact-head baseline/Alpha-secret checks and observed hosted disposable
runs on the combined candidate. `downstreamBrineAcceptance.status` remains
explicitly `not-run`: successful Dill-side supply is never presented as actual
Brine application/browser acceptance. No external Brine implementation is in
this lane's scope. SIGKILL cannot run Python cleanup; preserve
unknown state and inspect it instead of promising automatic resource restoration.
