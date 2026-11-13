# Disposable Alpha candidate evidence (fixture v1)

This is the **baseline harness** for [#80](https://github.com/umayr/dill/issues/80),
not completion of that issue, a deployment tool, or full conformance/release
qualification. A green baseline does **not** qualify the combined #77/#78/#79
pilot. `evidence.json` currently says `"qualification": "incomplete"` because
required combined cases remain blocked, even when implemented baseline cases
pass. The final acceptance gate can become complete only after every required
case has pinned disposable runtime evidence. Missing acceptance is represented
as `blocked` or `not-run`, never passed or silently skipped.

## Boundary and current subset

Use only a single-host, isolated, exclusively disposable Docker/Podman engine
with synthetic workloads and no real credentials or valuable data. An explicit
local Unix socket and `--disposable` operator attestation are mandatory for engine
mode. A socket path is **not** proof of host/daemon identity: it can proxy another
machine. The operator must establish isolation outside this baseline runner.
Never point it at the default desktop, production, Portainer, or homelab engine.
It does not provision or verify a sandbox, and it does not grant approval to run.

The runner uses the candidate binary directly for offline valid/invalid graph
fixtures and the existing `DILL_BINARY` integration override for seven selected
legacy real-engine tests. With `--operations`, it also requires explicit
`operation-capabilities` discovery of `dill.operation/1` and runs the executable
operation-envelope consumer. No core handlers or legacy exit/prompt contracts
are changed.

| Baseline | What the selected existing tests establish when actually run |
| --- | --- |
| Success/repeat/config drift | Lifecycle convergence and no-op plan/diff; config change reconciles |
| Image drift | Two synthetic image tags produce a recreation |
| Invalid config | Invalid unselected graph preserves existing stack identities/state |
| Foreign collision | A synthetic foreign container is refused and preserved |
| Storage | Teardown reports and retains a stack volume, rather than deleting it |
| Cancellation | Timeout and SIGTERM readiness cancellation exercises rollback cleanup |
| Partial failure | Failed start cleanup; not transactional restoration or data rollback |

These legacy engine tests are a baseline, **not** evidence of the qualified
operation/coordinator/disposable-profile APIs. Their Go sources are pinned by the
candidate commit and included in the downloadable artifact. Versioned offline
fixtures and the acceptance inventory are in `test/qualification/v1/`.

The prepared `TestAlphaOperationEnvelopeConformance` consumes only subprocess
JSON, not `internal/operation` types or mocked engines. It asserts complete
single-document envelopes, operation-ID uniqueness, explicit target digests,
plan versus diff exits, repeated/no-op container identity, changed-config recreate,
stop/start/restart, declined rm/teardown, explicit rm, retained-network failure,
invalid graph/no observed effects, failed-create unknown effects and post-create
readiness cancellation. It rejects leaked synthetic config/paths and null or
truncated observations. These are **prepared runtime assertions**, not recorded
runtime evidence until actually executed against the pinned candidate. They do
not establish complete foreign-resource preservation or coordinated recovery.

Every opted-in engine run selects required `TestAlphaNoPruneBothEngines`:
actual successful isolated CLI `up --no-prune` must preserve the omitted owned
service ID/running state, with coordination/profile flags deliberately ABSENT.
This row is explicitly bound to `engine` / `real-engine`, not a qualified orphan
rejection. The qualified profile separately forbids orphans and must reject them
with zero writes; that negative cannot stand in for positive no-prune.

With `--profiles` (which also requires `--operations` discovery), the runner
additionally selects `TestAlphaDisposableProfileDocker` only on Docker. It prepares verified
no-VOLUME images, asserts immutable-ID create and strict repeat/no-op identity,
preserves omitted services even with `--no-prune=false`, tests retained data,
foreign container/network/volume collisions, unsupported mounts/caps/secrets/
pull intent, locally prepared image-VOLUME and covered-VOLUME rejection, and
same-name replacement after captured removal confirmation. Rejected requests
must have no observed effects, no Docker mutation events and unchanged full
synthetic resource inventory/state/data. Negative Pkl fixtures must evaluate
successfully so parser failure cannot masquerade as policy enforcement.

These are **prepared profile assertions, not executed local engine evidence**.
The profile is not the #78 daemon-attested coordinator: profile runtime evidence
is stored only in the separate `preparedProfiles` observations and **cannot clear
any of the 24 required case IDs**, including bounded storage and qualified
image-volume rejection. Required positive no-prune is separately selected and
bound to its approved isolated CLI semantics, not promoted from observations. Known pinned-image/no-op
reconciliation corrections must pass the strict repeat expectation, not relax it.
The workflow enables these cases for a candidate containing the stable disposable
implementation; missing/skipped selected tests still fail. Full qualified mode
must later also test unsupported/foreign preflight **without** adding the separate
profile flag, so that flag cannot hide a coordinator preflight gap.

### Still required on combined final heads

`cases.json` lists the following explicitly blocked cases. Do not remove a block
merely because a prerequisite compiles or its mock tests pass:

- #77: operation discovery, exact contract invocation, success/failure/cancelled/
  unknown outcomes, redacted errors and accurate engine effects/resource reports.
- #79: explicit no-prune omitted-service preservation, bounded volume ownership/
  capability rejection **before any pull/write**, name reuse and explicit
  destructive intent after prompting. The proposed Docker-only disposable profile
  rejects binds, secrets, host namespaces, capabilities/devices and external,
  anonymous or explicitly named volumes. Positive qualified images must be
  prepared locally with effective pull never, and all image-declared VOLUME
  entries are initially denied, even with a matching named mount. Unknown image
  metadata cannot qualify; create must use the immutable inspected image ID.
  Approved isolated bind paths remain
  unsupported, not implicitly qualified. Podman profile rejection is not parity.
- #78: approved local layout, explicit expected daemon ID, trusted state directory,
  same-stack CLI/Brine contention, different-stack independence, alias handling,
  crash/unknown state and exact-operation acknowledgement after manual inspection.
  Proposed interfaces are not executed by this baseline; final CLI hooks and
  prerequisite fixtures must be reviewed before adding combined cases.
- Full foreign **container/network/volume** identity/state inventory around each
  failed preflight, cancellation, partial failure, concurrent operation and cleanup.
  The existing foreign-container case is not this complete inventory assertion.
- Brine end-to-end qualification using the exact transferred candidate checksum,
  source/schema/fixture versions and final integrated heads. A released v0.5.0
  image or another commit is not evidence for this candidate.

## Running offline (no engine access)

Requires Python >=3.9, the repository's Go toolchain and pinned Pkl. Use a clean,
trusted worktree, and keep build/evidence files in ignored `bin/` so they do not
make the candidate dirty. The output directory must not already exist.

```sh
python3 scripts/install-pkl.py "$PWD/bin/pkl"
make build VERSION="$(git rev-parse HEAD)"
make test
make vet
PKL="$PWD/bin/pkl" make validate
git diff --check

candidate=$(git rev-parse HEAD)
digest=$(python3 -c 'import hashlib; print(hashlib.sha256(open("bin/dill", "rb").read()).hexdigest())')
python3 scripts/qualify-alpha.py \
  --expected-commit "$candidate" \
  --binary "$PWD/bin/dill" --binary-sha256 "$digest" \
  --pkl "$PWD/bin/pkl" --output "$PWD/bin/alpha-evidence-offline"
```

`--allow-dirty` is solely for developing the harness. It records a development
limitation; its evidence is **not transferable candidate qualification**. Wrong
commit/checksum, mismatched embedded Go VCS revision, missing build provenance,
dirty source/build (unless explicitly allowed), wrong Pkl version or untrusted
Pkl executable digest fail closed. The Pkl executable must match one of the raw
release-asset digests in the candidate's pinned toolchain manifest; recording a
newer `pkl` from PATH does not qualify it. `dill.pkl` currently has no independent
version string: the evidence records that fact, its SHA-256 and source commit,
rather than inventing a schema release version.

The runner checks candidate/schema/Pkl hashes, checkout revision/status and
versioned fixture hashes again after execution. This detects common concurrent
changes, not malicious same-user substitution or all filesystem races. Embedded
build metadata and caller-supplied checksum are a **trusted build provenance
check**, not a signature or an attestation against an adversarial binary.

Exit 0 means **implemented baseline passed**, not pilot approval. Exit 1 means
input rejection, failed/missing/skipped selected tests, timeout, provenance
failure or baseline assertion failure. `baselinePassed` is separate from the
computed qualification/acceptance fields. Add `--require-complete` for the final
approval run: a green baseline with any incomplete required acceptance then exits
1. Missing/duplicate/extra required IDs, blocked/not-run/skip/failure, non-runtime
or wrong-candidate/target evidence, dirty provenance, incomplete final integrity
or missing supported-profile limitations all fail closed. There is no optional
case/waiver mechanism. Offline mode leaves engine cases `not-run`; it cannot
establish Docker or Podman runtime evidence. See
[combined runtime design](alpha-coordination-qualification.md) for the exact
required concurrency/crash/recovery inventory and unbound implementation gates.

## Opt-in real-engine runs and CI

Only after the operator authorizes a known disposable engine, add:

```sh
# Replace this example path ONLY with the approved exclusively disposable socket.
# Do not copy the default engine socket from ambient environment or context.
python3 scripts/qualify-alpha.py \
  --expected-commit "$candidate" \
  --binary "$PWD/bin/dill" --binary-sha256 "$digest" \
  --pkl "$PWD/bin/pkl" --output "$PWD/bin/alpha-evidence-docker" \
  --engine docker --socket /approved-disposable/engine.sock --disposable
```

Podman uses `--engine podman` with its explicitly approved service socket. Add
`--operations` only when deliberately qualifying the known operation contract;
unsupported/malformed discovery fails before any integration-test mutation.
Add `--profiles` to deliberately exercise the narrower prepared-image guardrail
suite too. Without these opt-ins, prepared cases remain blocked/not-run, never
implicitly passed. The
runner clears ambient engine selectors and credentials, uses a private HOME,
and pins both Dill SDK and direct engine CLI operations to the same endpoint.
Ambient `XDG_RUNTIME_DIR` is not inherited: private runtime/config directories,
an explicit empty `REGISTRY_AUTH_FILE` and private empty Docker config cover the
client auth-file fallback paths. Files are 0600 and directories 0700. This
isolates **client configuration**; the separately provisioned disposable daemon/
service must also have no operator credentials. A synthetic real-child regression
examines the client fallback paths with ambient credential sentinels present.
The runner invokes `make integration-test` with `DILL_BINARY` and a shell-safe
anchored selector assignment, and records every required top-level test; missing
tests and skipped subtests fail the baseline. Deadlines and runner SIGINT/SIGTERM
stop the active process group with TERM then unconditional KILL escalation, even
when the leader exited or a descendant closed its captured output. Interrupted
evidence is non-passing where it can be written. SIGKILL cannot be handled by
Python, and daemon-side actions or independently detached processes can outlive
any client. A timeout/crash can still leave resources needing manual inspection.
No automatic prune or broad destructive cleanup is performed by the runner.

The **Disposable Alpha baseline** workflow runs automatically on each pull
request's exact head SHA (not GitHub's synthetic merge commit), exclusively on
GitHub-hosted ephemeral engines. It also supports `workflow_dispatch`: dispatch
with a full 40-character `candidate_sha` after that exact commit is available on
the server; branch names/tags and a different `github.sha` are not substitutes.
The new workflow can collect PR evidence before it exists on the default branch.
It checks out and verifies that SHA, installs checksum-verified pinned Pkl,
builds/tests/vets/validates, then runs the selected cases in separate Ubuntu 24.04
hosted Docker and rootless Podman jobs. Every job's whole VM/engine is disposable.
Candidates carrying `internal/operation/testdata/manifest.json` automatically
select `--operations` in this workflow, and must pass discovery plus the executable
consumer; an old/malformed candidate cannot silently fall back to legacy tests.
The runner safely quotes the anchored test regex through both Make expansion and
the recipe shell. A regression executes the real Makefile with a harmless fake
Go executable to verify exactly one `-run` argument, including its dollar anchor.
The actual engine client/server version response is recorded; distro/SDK
versions alone are not evidence. These jobs have **not** been run merely because
the workflow file exists. The baseline matrix is not qualified profile support
on Podman, or the separate Alpha secret-runtime qualification.

Before approval, the supervisor must inspect a run pinned to the **final
integrated candidate** and its artifacts, and rerun after any follow-up change.
The additional `Disposable baseline / docker` and `Disposable baseline / podman`
checks must be reviewed on the exact candidate, not treated as issue completion.
All existing CI checks remain mandatory and unchanged: Format, Vet, Build, Test,
Integration / Docker, Integration / Podman, Validate Schema, Validate Examples,
Alpha secrets / docker, Alpha secrets / podman-rootful and Alpha secrets /
podman-rootless. This additional workflow does not replace those checks or prove
prerequisite acceptance. A watcher must verify exact heads and expected checks,
not rely solely on branch protection/mergeability or an old green run.

## Evidence, inspect, recover, clean up

The runner emits `evidence.json` with:

- exact commit, dirty status, binary version and SHA-256, embedded Go revision,
  Go version/OS/architecture, schema SHA-256, Pkl version/SHA-256 and toolchain hash;
- fixture version/checksums, requested explicit endpoint, disposable attestation,
  actual engine version response when run, exact commands, exits and output hashes;
- individual baseline case results plus unresolved prerequisite gaps.

Only bounded version metadata and command arguments are persisted; raw Pkl,
engine, workload or Go-test diagnostics are **not** copied into the evidence.
Do not put secrets in arguments, socket/config paths, fixtures or environments.
The private `home/` and raw daemon logs are not uploaded. This does not claim
arbitrary logs/legacy inspection/config output are secret-safe. On failure inspect
the last command/status, then reproduce it interactively **only inside the same
approved disposable environment**. Keep any raw diagnostics private. Normal CI
build/test logs are independently retained in the workflow run.

1. Stop issuing new mutations after cancellation, crash, unknown effect, identity
   mismatch or unexpected failure. Save the candidate evidence and workflow URL.
   Do not treat cancellation or a failed process as proof no effects occurred.
2. Inspect the explicit engine endpoint, daemon identity, synthetic stack labels,
   immutable container/network/volume IDs, states and mounts. Compare against the
   pre-run inventory. Confirm no foreign resource changed before claiming isolation.
   This full inventory demonstration is still a blocked combined acceptance case.
3. When #78 is integrated, inspect its trusted local journal and actual live
   resources using the **approved final inspection interface**. Unknown/incomplete
   records must block new mutation until exact-operation acknowledgement following
   inspection. No inspect/recover/ack command is invented here, and no automatic
   replay/rollback is promised by this baseline.
4. Existing test cleanup only targets its synthetic stacks. `down` retains
   networks; `teardown` retains volumes and networks and can exit nonzero to report
   retention. Cleanup is **not database rollback**, not restoration of replaced
   containers, and not a reason to remove destructive prompts.
5. On an authorized disposable host, review ownership and immutable IDs before
   manually removing any leftover synthetic resource. Never use system/container/
   volume prune, wildcards or shared image deletion on an inhabited engine. If
   isolation or ownership is uncertain, stop rather than infer safety from a name.
   Prefer destroying the entire dedicated test VM after preserving evidence; do
   not destroy an existing operator environment.

The workflow artifact includes the exact candidate binary, schema, toolchain
manifest, v1 offline fixtures, pinned integration sources, runner and evidence.
Before Brine consumes it, compare the transferred binary's SHA-256 to
`evidence.binary.sha256`, verify the expected source commit/schema/fixture hashes,
and require a clean candidate and actual supported runtime evidence. Keep the
workflow run URL/check outcomes alongside the artifact. No bundled-version bump,
production migration, real credentials, remote writes or epic closure is implied.
