# Required combined Alpha runtime cases (unbound design)

This is an execution design for #80, **not executed evidence**. Binding is blocked
until the supervisor supplies stable #77/#78/#79 composition commits and approves
the negotiated attested-target schema. The executable consumer remains strict:
recognize only explicitly documented, negotiated fields, distinguish ordinary
selector digests from qualified daemon identity, and never broadly ignore unknown
properties. Operation IDs retain the `op_` shape. Existing baseline engine parity
and Alpha secret CI remain independent mandatory checks.

## Fixture boundary

- One exclusively disposable native local Docker daemon, synthetic data and
  registry-free credentials/config, one fixed candidate SHA/binary/schema/Pkl and
  versioned fixture bundle. Do not reuse an operator desktop/production engine.
- Explicit `native-local-docker-v1` qualification, expected daemon ID, trusted
  state directory and `cli`/`brine` source. Exact flags follow the approved stable
  composition; the current proposals are not advertised as available commands.
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
| `no-prune` | Omit a live owned service, then repeat operation including retained volume and foreign same-name/name-reuse cases. | Explicit no-prune preserves omitted ID/state/data; preflight rejects foreign ownership before any pull/write; removal revalidates immutable intended IDs after prompts. |
| `bounded-storage-preflight` | Mount/capability/namespace/secret/image-policy violations; live resource census changes after prompt. | Bounded profile rejects before mutation; no orphan or uninventoried mount slips through; denied requests leave all snapshots unchanged. |
| `foreign-resource-inventory` | Check the dedicated inventory around **every** above scenario and cleanup, not only collision. | Foreign container/network/volume identity, state and data remain unchanged. Cleanup is not database rollback. |
| `brine-candidate-handoff` | Transfer exact binary+schema+fixtures and verify checksums, then invoke paired Brine consumer on the disposable target. | Same pinned candidate and negotiated subset, source brine contends with CLI, no released-old-binary substitution or claimed bundled support. |

## Evidence and completion gate

The current manifest leaves these rows blocked. Once implemented, each gets its
own actual selected test and real qualified-engine evidence, bound to this run's
candidate commit, binary digest and explicit target. The runner must supply the
approved qualified target/state interface rather than accepting a forged
`passed` fixture or optional evidence file. No manual waiver switches.

`--require-complete` makes incomplete final acceptance nonzero even when baseline
checks pass. The gate requires the exact required-case set (missing/duplicate/
extra IDs fail), every status passed, actual runtime provenance for each runtime
case, clean exact source/build identity, final integrity verification and stated
Docker-only profile limits. Qualified rows specifically require qualified-runtime
evidence; an ordinary engine test with a similar name cannot satisfy them.
Current blocked rows and unbound qualified modes cannot produce completion.

Unit tests exercise this predicate with synthetic input, including one complete
input to show it is not permanently hardcoded incomplete. Such unit input is
never published as runtime evidence. Final completion still requires independent
review, exact-head baseline/Alpha-secret checks and observed hosted disposable
runs on the combined candidate. SIGKILL cannot run Python cleanup; preserve
unknown state and inspect it instead of promising automatic resource restoration.
