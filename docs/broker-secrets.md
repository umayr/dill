# Broker-delivered static secrets (development contract)

This branch introduces `dill.broker-secrets/1`, independent of retired Alpha secret execution. The released v0.11.0 binary does not advertise this contract. Provider authentication and resolution belong to Brine, never Dill agents.

## Current implementation boundary

`broker-requirements --contract dill.broker-secrets/1 -f /absolute/captured/compose.pkl -p stack` performs value-free source requirement discovery. It evaluates trusted captured Pkl with bounded machine diagnostics and returns logical names, per-service file grants and a canonical evaluated configuration digest. It is not a sandbox, target qualification, secret resolution, plan approval or permission to mutate. The host must independently verify and hold the complete source capture and target enrollment identity. No provider values enter evaluation or discovery output.

The development engine/planner APIs `WithBrokerSecrets`, `StartServiceBroker` and `ComputeBroker` require opaque target-store materialization; public ordinary methods still reject secret declarations. These APIs reuse the native adapter and plan implementation. Callers must hold the existing ownership/coordination checks and qualify the actual engine identity. Endpoint digest comparison alone is not daemon identity proof.

Broker review now has its own `dill.broker-selection/1` digest domain, wrapping the validated native selection and exact source/config/target/policy/binding/generation/handle scope. Existing ordinary review digests cannot substitute for it. The shared producer and private inventory have explicit broker modes; neither captures live facts or authorizes execution without the caller's retained qualified lease.

Broker-created secret consumers additionally carry opaque registration, generation and target-handle labels. Labels alone never authorize adoption: protected live facts require an authenticated active generation in this target's private store, and bind mounts must match that stored service's exact read-only files. Retired strict-secret workloads and forged/missing broker markers are refused. Ordinary named preflight retains its original protected-workload denial.

The private coordination bridge now has an explicit broker mode: it checks source configuration, whole resource inventory, authenticated live secret files and the qualified physical endpoint/daemon before constructing a broker-capable native Docker adapter. Broker and retired disposable modes cannot be mixed. The binder compares the broker-domain selection and managed ownership, installs immutable image restrictions, and activates runtime material before the existing captured DAG path can mutate. Read-only capture still refuses broker creation. Podman now has a separate retained transport/identity qualifier and SDK-bound adapter constructor, and namespace/shared-path guards are now wired into native adapter enforcement, and private profiles now flow through native broker coordination/review/binding and CLI selection. Agent local/HTTPS operator profile propagation is wired; Brine consumer enrollment/profile pins are wired; default rootless namespace handling is wired; code/contract checks are separate from outstanding runtime qualification. There is no unqualified transport fallback.

## Namespace-bound rootless implementation (runtime not qualified)

The chosen ownership model is an operator-started executor in the engine user
namespace, not a new privileged writer. Web/source requests never request
namespace entry or host UID/GID ownership. Mapping commitments are bounded,
canonical and independently provisioned; observations must match both UID/GID
maps AND the actual namespace inode/device. Rootless maps cannot expose host
root IDs; unmapped grant owners are refused. Linux peer observation uses the
retained Unix connection credentials and descriptor-anchored process metadata.
Non-Linux observation fails closed.

A separate namespaced Docker qualifier now binds the daemon identity to the
operator map commitment and checks retained rootless metadata plus kernel
namespace evidence. Namespaced native Docker construction now requires a fresh
authenticated generation, mapped grant owners and kernel-backed shared-path
checks. Broker CLI/capture now propagates the private operator profile into exact
qualified adapter selection. Default namespace handling is wired; native/synthetic evidence is not positive Linux end-to-end qualification. Podman transport qualification now binds operator-provisioned machine ID,
hostname, graph root and namespace map commitment. Production Linux qualification
verifies retained socket-peer namespace and local machine identity; tests inject
only synthetic peer observations. The constructor binds the real SDK to that
retained client after its non-authoritative initialization ping and replaces
bootstrap version metadata with the qualified observation. The transport is
poisoned after failure so the SDK retry loop cannot replay an uncertain mutation.
Native adapter enforcement now requires the same user AND mount namespaces and
filesystem root for executor and engine. Qualified file paths must be canonical,
no-symlink descendants of the authenticated private generation root; exact type,
mode, single-link ownership and mapped grant IDs are checked. Namespace/root
views are checked before and after path observation and against the initial
snapshot. The target digest commits the mapping profile; private generation MACs
bind that target scope, without rewriting owners into unreviewed host IDs.

These stricter profiles require an operator-started executor in both engine
namespaces and the same root view. They never enter namespaces automatically.
Native constructor, guardrail preflight and service creation repeat the checks;
empty retirement still verifies the generation root. Native capture and CLI profile propagation are wired. Brine policy propagation and explicit default namespace handling are wired.
Runtime support remains unqualified until positive disposable-target checks.

## Default container namespace boundary

Qualified broker Podman creation explicitly sets the engine-user-namespace
(host relative to Podman), rather than inheriting keep-id/auto/private mappings
from ambient engine defaults. With a qualified rootless engine this is its
verified rootless namespace, never initial-host root. Ordinary contexts are not
changed. The pinned Podman SDK5.0.0 inspect producer reports no additional OCI
user namespace as empty UsernsMode; custom maps remain raw in inspect/selection.
Broker Podman inspection requires the exact raw UsernsMode field from the same
single qualified response before SDK decoding; missing/null fields, duplicate
or case-alias keys and incomplete custom-map objects are unavailable evidence,
not an observed default. Responses are bounded to 8MiB; no second inspection or
retry supplies substitute facts. Ordinary SDK inspection is unchanged.

Both initial census and repeated broker inventory require known HostConfig,
empty UsernsMode and no custom UID/GID map entries. Private/keep-id/foreign
namespace paths and nonempty/contradictory mappings remain denied, not normalized
away or assumed equivalent to the operator mapping profile. Docker rootless uses
its daemon-default user namespace under the retained profile/shared-path checks.
Qualified Docker inspection also checks the bounded single raw response before
SDK defaulting; missing/null/duplicate/case-alias namespace fields refuse. Its
retained transport, timeout and redirect policy are preserved, with no fallback.
Only this default no-child-remapping profile is implemented. Synthetic specgen
and raw-fact denial tests are not Linux runtime qualification.

## Operator profiles and broker-only review contracts

`dill.broker-profile/1` now has a bounded private operator-file loader. It requires
exactly one Docker or Podman qualification, rejects unknown/duplicate JSON fields,
ambiguous engines, cross-engine layouts and invalid maps, and derives the exact
offline enrolled target digest. File admission requires canonical no-symlink
paths, one regular private0400/0600 file owned by namespace UID0, and at most64KiB.
The file is operator configuration, never a captured source file or HTTP field.
Rootful Docker keeps its existing enrolled identity; namespace and Podman profiles
bind their independent mapping/storage identity.

Broker-only invocation syntax supports the new layouts and Podman without an
invented Docker daemon UUID, with up/no-prune/never-pull constraints. Effective
broker inputs use `dill.broker-effective-input/1`; broker selection separately
validates engine-bound inner facts. Ordinary invocation, target/input validation
and digest domains remain unchanged and continue to reject Podman profiles.
Parsing/commitment validity alone grants no execution authority. Non-init broker
commands may use `--broker-profile /absolute/private/profile.json` INSTEAD OF
`--engine-socket` and `--coordination-daemon-id`; mixing them is refused. The file
is independently loaded at each invocation. The value-free offline command
`broker-profile-info --contract dill.broker-profile/1 --broker-profile /absolute/private/profile.json`
exports only engine/target/endpoint commitments and runtimeOutcome:not-run. It
loads authenticated operator metadata but does not contact an engine, evaluate
Pkl, read provider values, provision state or establish runtime health. Offline
export never establishes live qualification; Linux store/profile checks and the
actual paired execution cases are reported separately below. The exported target/endpoint pins are
used in Brine local policy or the independent HTTPS broker target commitment. Its offline target/config/source
scope must match the reviewed generation, then its live target is qualified under
retained coordination. The snapshot, binder and adapter use that exact engine.
A detached private profile copy is passed only alongside broker material; ordinary
review/apply cannot request this path. New result validation is broker-specific;
ordinary validation remains unchanged. Agent child invocation now forwards the operator profile instead of legacy
socket/daemon overrides and validates broker-specific review output. Brine local policy now forwards explicit profile/engine/target/endpoint pins;
HTTPS policy can separately pin the broker target while retaining complete monitor
enrollment. Broker-only review decoding accepts the exact scoped engine; ordinary
preflight decoding is unchanged. Paired native offline journal/fence/status tests
and isolated signed-TLS/browser flows establish contract behavior, not runtime
qualification.

`broker-local` accepts the profile path with explicit operator `--engine-kind`,
`--target-digest` and `--endpoint-digest` pins instead of legacy socket/daemon.
Non-recovery actions independently load the profile and match all pins. Journal,
status and fence use the same durable pinned identity without opening a missing
profile or runtime store. Retargeting cannot reuse a journal or reset history.

HTTPS may opt into `--https-broker-profile` only with an existing broker store
and a Docker profile matching the existing monitor enrollment socket/daemon.
Namespace commitments apply only to broker execution; monitor authority is not
silently repurposed. Podman is explicitly rejected on this Docker-only HTTPS
agent, not advertised as remote Podman support. Route credentials/grants,
strict HTTP fields, source catalog and pending-before-detached-execution remain
unchanged; no profile path or runtime control is accepted from HTTP.

## Explicit local broker commands (development; scoped runtime evidence below)

The integration now includes strict `broker-init`, `broker-prepare`, `broker-preflight` and `broker-managed-operation` namespaces. These are operator/agent boundaries, not browser APIs. Unknown/duplicate flags, legacy options, verbosity, relative/noncanonical paths, extra stdin fields and duplicate JSON keys are refused. Values only enter prepare's bounded stdin envelope; replies contain metadata only. Provisioning is explicit and never a fallback for missing state.

All commands require `--contract dill.broker-secrets/1 --broker-store /absolute/private/store`. In legacy Docker mode, non-init commands also require `-f /absolute/captured/compose.pkl -p stack --engine-socket /absolute/docker.sock --coordination-daemon-id ID --coordination-state-dir /absolute/private/coordination --source-digest sha256:…`. Paths, daemon ID and source digest must come from trusted operator configuration / independently verified captured source, never an HTTP payload. Pkl is executable operator-trusted source: machine evaluation bounds/redacts diagnostics but is **not a sandbox**. Do not accept tenant/untrusted Pkl as an isolation boundary around host runtime files or keys. Profile-backed non-init commands instead select the exclusive private `--broker-profile`; source and coordination flags remain required. Both legacy rootful Docker and explicit qualified profiles never pull and disable prune. It does not support privileged services, jobs, hooks or arbitrary host binds. Legacy rootful Docker preparation requires positive Linux/security metadata and refuses rootless/remapping. Explicit namespace profiles additionally require mapped grant owners and shared-runtime proof before preparation or native creation. Only the explicit default container user namespace is admitted; custom child mapping modes remain refused. Explicit operator profiles wire qualified Podman and namespace-bound Docker; actual scoped Linux execution evidence and remaining limits are listed below.

Prepare reads the private wire envelope; it checks actual evaluated config/grants and qualifies the target before creating a pending generation. Review reads flat metadata `{"scope":…,"handle":…}`. Its reply wraps native preflight plus native Plan under the broker contract and binds the outer broker selection digest. Managed execution additionally requires `selectionDigest` and a valid native `ownership` expectation (create/update only). It reopens the exact generation, repeats capture/ownership comparison under the retained target lease, activates before mutation, and uses the existing captured DAG, operation recorder, coordination journal and managed receipt. The native guard delegates creation through the explicit broker creator, not ordinary unchecked service creation. A handle is not standalone authorization.

## Opt-in HTTPS agent contract (development)

Combined Linux Dill can opt in with `--https-broker-store` pointing to an existing private Linux runtime store (namespace UID0 for explicit namespace-bound profiles), alongside the existing preview/apply/staging configuration. The store must be disjoint from other agent directories; the existing private deployment journal must already be present. Startup refuses missing used state, and never provisions/resets it automatically. Broker routes remain absent without this configuration. Pkl source and the Pkl executable must be operator-trusted; this is not tenant sandboxing.

Each route requires TLS, the separate deploy credential and its exact explicit `--https-deploy-grant`. Monitor credentials and ordinary deploy grants confer no broker access:
- GET `/v1/broker-secrets/journal` — broker-status. Returns expected target/physical endpoint commitments and the journal ID (not a live target qualification).
- POST `/v1/broker-secrets/requirements/{stageId}` — broker-requirements. Body: stack and manifestSha256. Only typed value-free logical names/grants/config digest are returned.
- POST `/v1/broker-secrets/prepare/{stageId}` — broker-prepare. Body: the explicit private delivery wire envelope. Replies contain only scope/handle/expiry.
- POST `/v1/broker-secrets/review/{stageId}` — broker-review. Body: scope + handle metadata. Returns native preflight/Plan and broker selection digest, with managed create/update mode.
- POST `/v1/broker-secrets/apply/{stageId}` — broker-apply. Body: scope, handle, requestId, selectionDigest and confirm (create/update). No paths, executable, socket, environment or ownership list is accepted from HTTP.
- GET `/v1/broker-secrets/operations/{requestId}` — broker-status. Returns raw conservative status plus immutable reference when present. A fenced unadmitted request has no reference.

All require exactly one `X-Dill-Broker-Contract: dill.broker-secrets/1`; except journal discovery, they also require the exact `X-Dill-Journal-Id`. POST content type is exactly application/json. No query strings, encoded path variants, extra fields or duplicate JSON keys. Stage ID is a single canonical ID segment. The manifest is independently verified by the catalogued stager before each child invocation. Subprocess argv consists only of trusted operator paths/identity and value-free metadata; private bytes enter bounded stdin. Child environment is isolated.

Review and apply share the ordinary deployment mutation slot. Apply repeats review/ownership checks, durably reserves the existing deployment journal, then detaches cancellation for bounded execution. Lost/invalid child evidence stays unknown and blocks retries. Managed receipt rechecks authenticated secret mounts and exact applied generation. Existing journal fence/status recovery is reused with its separate explicit fence/status grants; never silently resubmit an uncertain request.

The first broker admission upgrades the private journal to version 3, retaining value-free broker scope/handle metadata through compaction and restart. Versions 1/2 remain readable; older binaries fail closed on version 3, so preserve rather than reset/downgrade used state. Any broker intent permanently blocks ordinary preview/apply/removal for that stack, including failed-no-effects intents. No source Restore or legacy mutation is used as a secret recovery operation.

## Administrator-local adapter

`dill broker-local ACTION` is a no-listener administrator CLI over the same
catalogued stager, broker endpoint, qualified native child and deployment journal.
Actions: init, journal, stage, snapshot, requirements, prepare, retire, review, apply,
status and fence. There is no second executor or ordinary mutation fallback.
All require explicit contract, either native socket/daemon or private profile plus
engine/target/endpoint pins, coordination state, runtime
store, history directory, staging root/catalog and Pkl executable flags.
Action IDs and the expected journal are separate strict metadata flags; values
and source-upload bytes enter stdin only.

Init requires an existing runtime store and creates a NEW history directory
exclusively. Every other action requires an existing private operations file:
missing state is never initialized. Status/fence do not require runtime material
or source catalog availability. Concurrent invocations contend on the same
exclusive journal descriptor; a busy/lost child is unavailable/unknown, never
permission to resubmit. The permanent caller request ID is admitted before the
native managed child can mutate. Source capture is independently verified by the
same catalog used for HTTPS. Local and HTTPS journals have distinct target
domains and cannot silently adopt one another's ownership.

Brine has a grant/current-policy-gated local CLI client and HTTPS client. Paired
native CLI tests cover local journal, durable fencing/status and synthetic
staging/manifest retrieval without Pkl/provider/engine execution. Application
startup/controller/history and production Settings/Variables/Plan/History are wired
in the paired Brine development worktree. Synthetic coverage is supplemented by
actual file/Vault acquisition and Docker/Podman/rootless delivery, update/recovery
and reviewed retirement cases below, plus a production browser-to-real-Docker flow.
Unavailable evidence still fails closed. These are unreleased development contracts,
not features of published Dill0.11.0 or Brine's currently bundled release. Never
route through ordinary or retired execution to work around qualification limits.

## Generation identity

The wire scope binds deployment registration, effective stack, engine, qualified target digest, explicit endpoint digest, captured source digest, canonical evaluated config digest, binding revision, operator-policy revision and Brine generation ID.

Broker evaluation optionally reads `.brine-broker-inputs.json` beside the captured entrypoint. This is **non-secret configuration source data**, not a secret envelope or HTTP environment option. The unpublished `dill.broker-inputs/1` document admits only validated `BRINE_VAR_` keys, bounded counts/sizes and canonical environment mapping. Source and evaluated-config digests commit to these inputs. Missing input files never inherit ambient `BRINE_VAR_` values; malformed, oversized, symlink or nonregular inputs fail closed. Only operator-controlled evaluator infrastructure (PATH/HOME/TMPDIR/certificate paths) is inherited. Inputs cannot set executable/loader paths, engine sockets, credentials or other arbitrary target environment. Brine reserves the filename, includes the generated document in the complete consented source snapshot, displays names/commitments rather than inline input values, and rechecks effective variables under its dispatch lease. Ordinary/retired commands do not gain broker execution authority from this file. The target returns a distinct unpredictable handle. Source requirements and grants are supplied independently from the captured source, never inferred from a receipt or provider response.

The Linux target store must be explicitly provisioned on a fresh private runtime directory. Open never recreates missing used state. Files are private, immutable per-service generations mounted read-only, with target-checked ownership/mode and a private length-framed HMAC. Prepared generations expire in five minutes. Apply must durably Activate before engine mutation; active material remains retained on uncertainty. Capacity exhaustion fails closed; no automatic active-generation deletion exists in this slice. Namespace-bound rootless materialization uses namespace UID0 with mapped grants and the authenticated shared-path checks above, not an initial-host-root writer. Non-Linux materialization is unsupported, never a fallback.

Broker creation uses the existing generic `dill.secret-enabled` protection marker so ordinary/direct-start/diagnostic denial paths retain their classification. This does not enable retired strict-secret invocation or its policy/secretfile machinery. Per-service hashes contain opaque registration/generation references, never bytes, provider paths or value hashes.

## Explicit reviewed retirement

The separate unpublished `dill.broker-retirement/1` request contains a new scope
and exact previous generation references, never secret items. Ordinary secret
Envelope zero-item denial is unchanged. Target preparation authenticates each
predecessor MAC and active scope in the same registration/stack/engine/physical
target group, then writes a separately identified MAC-bound empty manifest. It
never deletes/deactivates previous generations, volumes or provider values.

Native broker inventory review now checks full current stack membership,
ownership and authenticated exact mounts. Retirement restricts live generation
adoption to its explicit predecessor list (or its own authenticated active handle).
An exact empty evaluated config is required. Omitted or duplicate workloads fail
both review and pre-mutation guardrails. Empty-generation workloads keep broker
protection labels, and the managed receipt requires the newly applied handle and
generation. Retirement preparation is exposed separately as `broker-retirement-prepare`,
`broker-local retire`, and POST `/v1/broker-secrets/retire/{stageId}`. HTTPS
requires the separate `broker-retire` deploy grant; prepare/review/apply/monitor
grants do not imply it. The target journal must identify a managed broker
predecessor included exactly in the retirement request. Returned preparation is
metadata-only and does not deploy; the existing broker review/apply/status path
still performs native coverage/ownership/selection checks and durable admission. The confirmed policy
is **no prune**: omitted secret-using workloads BLOCK retirement and must be
resolved by retained-source mount removal or a separately reviewed lifecycle
operation. Brine controller, unified Plan/confirmation and shared History are
connected and synthetic-browser-tested. Preparation alone never authorizes mutation.
Linux private-store checks and actual reviewed retirement passed on the scoped
runtime cases below; historical resources and protection were retained.

## Verification limits

The paired development worktrees were exercised on disposable Debian13/Linux6.12.118
amd64 with Pkl0.31.1, Docker26.1.5, Podman5.4.2 and RootlessKit2.0.2. Each following
provider/transport case passed real container-byte consumption, unauthorized UID
and read-only denial, history non-disclosure, a real update with deliberately lost
successful response, no replay, app restart/original-request reconciliation, and
reviewed empty-generation retirement (process exit0):

- Rootful Docker: file/local, Vault1.20.4 KVv2/local, file/HTTPS and Vault/HTTPS.
- Rootful Podman: file/local.
- Rootless Podman: file/local in the explicit private-root arrangement below.
- Rootless Docker: file/local and file/HTTPS, preserving full signed Docker monitor
  enrollment independently of the broker namespace target commitment.

The production Brine Settings/Variables/native Plan/confirmation/shared History
browser path additionally passed real file acquisition and Docker delivery, with
independent container-byte verification and mobile/dark plus desktop/light evidence.
Its fixture uses mutually authenticated TLS and production session authentication;
it is not a standalone login/dashboard test. Full evidence/fixture contracts are
in Brine's `docs/broker-runtime-qualification.md`.

Rootless testing first correctly refused normal host-root ownership, which is
unmapped inside the user namespace. No overflow-UID exception was added. Operators
instead explicitly started matching engine/executor user/mount namespaces with a
namespace-UID0-owned private filesystem root/state and `pivot_root`, read-only
system-file mounts and fresh per-root machine identity. Public Podman unshare or
RootlessKit bootstrap precedes the root-view switch. There is no request-triggered
entry/escalation or privileged secret ownership helper. Child CLIs use empty private
homes, not ambient source/operator engine configuration. Podman creation explicitly
selects private IPC; existing shareable/unknown evidence remains denied.

A subsequent separately hosted Infisical v0.167.16 instance passed the fresh sandbox
Brine Settings/Variables/Plan/confirmation/History path, exact container-byte
verification and provider viewer-write denial403. Its complete recovery/retirement
matrix is not claimed; see Brine's runtime qualification record. This is not
an exhaustive provider/profile/transport cross-product: Vault on Podman/rootless
executors and real Git/Restore browser runtime are not separately claimed. Remote
Podman remains unsupported on the Docker-only HTTPS agent. Existing sandbox stacks,
default contexts, global sysctls and persistent-service configuration were not changed;
separately approved prerequisites were installed through audited `./hl` actions.
All failed/unknown outcomes and historical resources remain retained. These scoped
development results do not claim published/released support.
