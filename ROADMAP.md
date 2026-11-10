# Dill roadmap: typed application stacks, deliberate operations

Status: **implementation vision and experimental contracts**, not a list of shipped features. The checked-in `dill.pkl` remains the supported schema. Examples under `docs/design/examples/` are evaluated Pkl authoring prototypes; current Dill MUST NOT be expected to deploy their output. No `@NEXT` package exists.

Companion: [Brine roadmap](https://github.com/umayr/brine/blob/docs/roadmap-architecture/ROADMAP.md). The [coordinated ticket index](docs/design/TICKETS.md) links every implementation issue and its prerequisites. Implement the ticket contract, not an earlier speculative chat example. If a design cannot be implemented safely, report the conflict instead of silently approximating it.

## 1. Product and audience

Dill is a standalone, CLI-first application stack manager for people operating persistent applications on one or a few explicitly chosen container hosts: homelabs, self-hosters and small internal deployments. Brine is its optional self-hosted control plane. The objective is **typed reusable intent, useful preflight, understandable change plans, and predictable execution/recovery**, not a type-safe spelling of every Docker Compose key.

Compose/Portainer configurations are evidence of application requirements and migration fixtures, not the native API specification. Compose has a much more mature ecosystem and already offers dry-run, dependency conditions and configuration reuse. Dill must earn adoption through operational clarity, not claim those features are unique. Pkl-generated Compose remains a legitimate alternative if only typed authoring is needed.

We do not initially build a scheduler, Kubernetes replacement, VM/LXC system manager, host provisioner, backup storage engine, secrets vault, or mandatory registry. Nix/Ansible remain useful for host configuration; systemd/Quadlet remain useful supervisors; existing backup, identity and observability systems remain integrations. Portainer/Dockge are the nearest UI/workflow alternatives, not Kubernetes.

## 2. Boundaries that implementations must preserve

| Responsibility | Owner | Consumer / boundary |
|---|---|---|
| Pkl evaluation, normalization, versioned semantic IR | Dill | Brine consumes results, not a second parser/model |
| Package resolution, dependency graph and checksums | Pkl | Dill adds recipe contract/discovery conveniences, not another solver |
| Application DAG, preflight, plan, ownership, apply, cancellation | Dill | Identical semantics from CLI, CI or Brine |
| Runtime adapters and target capabilities | Dill | Brine must not duplicate each adapter |
| Source workspace, Git polling, UI/API, scheduling, approvals | Brine | Invokes explicit Dill operations |
| Execution/job outcomes and target-local serialization | Dill | Brine persists/indexes redacted history for the operator |
| Secret resolution and operation-specific backup execution | Dill + approved providers | Brine edits bindings/schedules; not a vault or backup engine |
| Web identity, notifications, fleet inventory | Brine | Not required for standalone Dill |
| Provider installation, authorization, trust policy | Operator | A recipe cannot grant itself privileges |
| Host/VM provisioning, DNS/proxy and file synchronization | Existing tooling | Integrate explicitly; do not silently take ownership |

For managed workloads, Brine mutations must go through Dill. Engine-wide inspection is allowed; unmanaged administrative mutations require a separately explicit, audited mode. Brine's process-local path mutex is not sufficient concurrency protection. Target/stack identity must be consistent across viewers, lifecycle, logs and exec. A remote executor should make target-side filesystem checks, secret resolution and locking authoritative.

## 3. Current foundation and limits

At the roadmap baseline, Dill has Docker/Podman adapters, field-level plan/diff, ownership checks, dependency-ordered startup, resource limits and created-container failure cleanup. It does not yet implement the proposed Application/Deployment model, durable jobs, saved approved plans, or full restoration. Cleanup after failure is **not rollback**: old containers may already have been replaced and databases may have migrated.

The current graph is built before engine mutation; do not repeat historical claims that cycle detection still necessarily follows orphan removal. Audit current source before acting on old issue descriptions. Several existing issues have been rewritten to remove stale claims (for example, resource limits already exist).

Brine currently shells out to Dill for stacks and directly accesses an engine for other operations. It has a local agent and snippet recipes, not a completed multi-host executor or package manager. Its separately configured engine socket must be propagated into Dill execution, not merely checked for matching engine type.

## 4. Configuration model

Separate four kinds of information:

1. **Application intent:** services, jobs, relationships, storage/secret requirements, readiness and resource requirements. Usually Pkl in Git.
2. **Deployment bindings:** explicit target, actual host paths/volumes, published addresses, secret references and environment-specific values. May be a one-file amendment or separate Pkl module.
3. **Trusted tool/operator configuration:** contexts, provider registrations, host mount assertions, effective authorization. A project cannot override this trust boundary.
4. **Operational state:** resolved candidates, outcomes, job records and history. Not application source; never contains plaintext secrets by default.

Use camelCase, classes, constrained types, and typed `Listing`s with explicit `name` properties for public declarations and bindings: services, jobs, storage, secrets, devices and ports. Use `Mapping` for naturally keyed values such as environment variables and labels, not empty keyed declarations such as `["wireguard"] {}`. Amend component objects before inserting them into listings; do not patch services by list position. Do not use shell-string parsing or unstructured option bags as the canonical interface. Prefer disjoint classes for mutually exclusive networking/storage forms. Normalize quantities to explicit units in a versioned JSON IR; Pkl `Duration`/`DataSize` require deliberate conversion. Unknown IR versions must not bypass validation.

The examples demonstrate this syntax:

```pkl
import "Model.pkl"
import "Postgres.pkl"

local database = new Postgres.Component {
  name = "catalog"
  database = "catalog"
  storage = "catalog-data"
  password = "catalog-password"
}

local catalogService = (database.service) {
  resources { memory = 512.mib }
}

// Within application.services: insert catalogService as a Listing element.
// services { catalogService }
```

Read the executable [two-component fixture](docs/design/examples/composed.pkl) rather than treating this fragment as a runnable module. The same component is instantiated twice; one instance is amended without modifying the package. Libraries must agree on a compatible canonical schema dependency. Cross-package type identity and Pkl minimum-version selection must be tested, not assumed.

Declaration order is authoring order, **not startup order**. Normalize named listings into internal indexes only after rejecting duplicate names (including service/job collisions in their shared graph namespace), missing required bindings and conflicting bindings. Never use last-write-wins map construction. Reordering declarations alone must not change semantic hashes or recreate workloads; preserve order where it is meaningful, such as process arguments. DAG edges alone determine lifecycle ordering.

**Types are not all validation.** A typed `String` reference still needs semantic lookup. Pkl checks shape/ranges; Dill checks references, uniqueness, graph cycles, effective policy, target support and observed state. Design fixtures do not yet implement those Go validators.

### Services, jobs and relationships

Services are long-running workloads. Jobs complete with declared successful exit codes. Initially job triggers are `manual` and `eachDeployment`; do not introduce an ambiguous `runOnce`. A manual prerequisite requires an explicitly selected execution or an applicable successful record. Unknown outcome after a crash is not success and is not permission to rerun non-idempotent initialization.

The effective DAG includes declared dependency edges **and implicit edges** from namespace sharing and other required relationships. Service/job IDs must be unique in the graph. Conditions are `running`, `healthy`, and `completed`; completed gates refer to jobs. Optional edges cannot weaken hard resource/security dependencies; even optional edges cannot create graph cycles. Startup is prerequisite-first with bounded concurrency, stop/removal dependent-first. Application traffic may be cyclic; the lifecycle dependency graph may not.

Separate readiness from restart policy. Image-defined health checks count only after their availability is verified. Explicit command probes specify their execution location. Do not run arbitrary host shell probes during read-only validation.

Partial operations compute required/affected closure and retain unrelated workloads. An escape hatch such as `--no-deps` cannot silently bypass hard namespace requirements. Replacement propagation (`none`, `restart`, `recreate`) is distinct from engine crash restart policy. Namespace owner replacement requires recreation or explicit failure; restarting a dependent is not sufficient.

### Concrete network example

[downloads.pkl](docs/design/examples/downloads.pkl) models a VPN and consumer:

```pkl
network = new Model.SharedNetwork {
  service = "vpn"
  condition = "healthy"
  onOwnerReplacement = "recreate"
}
```

Expected plan: replacing the VPN includes recreation of its namespace consumers, with the reason shown. Validate missing/cyclic references and incompatible port/attachment declarations before disruption. Readiness is checked after startup; preflight cannot prove a VPN works or cannot leak traffic.

### Storage and secrets

Storage requirements bind to host paths or existing/managed volumes. Existing resources are retained and non-owned. A missing host path defaults to failure, not implicit directory creation. Expected mount identity and effective UID access are checked on the engine host, or reported UNKNOWN when unavailable. A source change is a potential data migration, not evidence that Dill will move data. No parallel writable production database instances during pilots.

Secrets remain `{provider, key}` references through rendering, plan and history. Resolve late, preferably on the target. Support file delivery and explicitly documented environment delivery; environment injection is not hidden from engine inspection. Never put raw secret hashes in labels as a change detector. Use opaque provider versions or explicit rotation tokens, with defined behavior for unversioned providers. Separate application secrets, registry/Git credentials and Brine authentication.

Requested policy is not authorization. Operator policy is authoritative, and deployment policy can only tighten it. Recipes cannot enable privileged execution, register providers or permit data deletion. Experimental orphan handling defaults to retain/explicit approval; legacy default changes require a versioned migration. Removing a declaration does not automatically authorize deletion.

## 5. Staged validation and operation contract

| Stage | Allowed behavior | Required result |
|---|---|---|
| Offline evaluation/semantic validation | Parse/evaluate under configured Pkl trust controls; normalize, validate references/DAG | No engine deployment mutations |
| Read-only target preflight | Inspect ownership, capabilities and observable prerequisites | PASS / FAIL / UNKNOWN / DEFERRED with evidence |
| Explicit preparation | Authorized pulls/builds and required preparation before disruption | Resolved artifact identity and failures |
| Candidate and approval | Bind intent, target, policy and critical observations | Reviewable digest; not an eternal guarantee |
| Apply | Acquire target/stack lock, revalidate critical assumptions, execute graph | Structured progress and honest final/partial outcome |
| Verification | Readiness/application checks requiring running workloads | Evidence, recovery options, limitations |

UNKNOWN is not green; DEFERRED names its future check. Missing image metadata may remain unknown until explicitly prepared. A port availability check cannot eliminate races. Policy determines blocking/approval behavior. Artifact preparation is not hidden inside read-only `validate`.

A rendered artifact is not an approved plan. Approved candidates bind exact image identities and critical preconditions; stale state or changed input invalidates approval/requires replan. Pull required images before stopping healthy containers. Do not refresh catalogs or select new tags while applying an approved candidate.

Protocol results need version/capability negotiation, operation ID, target identity, structured errors, progress and final status. JSON stdout must remain machine-readable; diagnostics belong on stderr. Preserve `plan` nonzero-on-change versus `diff` zero-on-drift. Cancellation and partial cleanup failure must be observable. Cross-process identity locking must cover CLI and Brine; a client-local lock is not a distributed lock.

## 6. Open recipe distribution

Use standard Pkl packages over HTTPS with immutable metadata/archives and verified checksums. Anyone may publish. A static JSON catalog maps qualified source/name/version to canonical package URIs; direct URIs and local project dependencies work without any catalog. No official service is required for execution.

Components export typed configuration; blueprints assemble complete applications; snippets are copied text without dependency tracking. Keep these distinct. Generated projects remain ordinary editable Pkl. Use `PklProject` and `PklProject.deps.json`, not a second resolver or lockfile. Pkl's minimum-version selection can select higher declared versions within a package major; do not claim every requested transitive version is exact. First-use checksum trust is not publisher authentication.

Specify source precedence/collisions, bounded parsing/downloads, explicit refresh, caches, errors and withdrawal. Search never evaluates publisher-supplied code. Publish archive/metadata, verify from a clean cache, then advertise in catalogs. Authenticated hosting/offline/mirror support requires separate tested contracts, not assumed alpha promises.

First success criterion: **two independent publishers, two components, one stack, zero required central services**. Recipe upgrades are explicit reviewed dependency changes.

## 7. Providers, recovery and future targets

Start with narrow trusted secrets/backup providers before a general extension platform. Out-of-process protocols need version negotiation, capability limits, timeouts, cancellation and redaction. Processes are not a sandbox. Recipes cannot install executable hooks. Brine owns notifications/webhooks/schedules; Dill owns operation semantics. Runtime adapters remain built in until their contract is stable.

Integrate application-consistent backups using existing tools. Database dump/quiesce requirements differ from archiving live files. Verify restores into isolated stacks and record evidence. Previous container configuration restoration cannot undo arbitrary database migrations. Recovery must communicate what is and is not reversible.

Docker/Podman remain primary. Explore Apple `container` only after capability contracts stabilize, using documented APIs/CLI and a full stateful lifecycle test. OCI images do not imply Docker API equivalence. Investigate Incus OCI application containers separately from LXC system containers/VMs. No automatic host provisioning. Evaluate systemd/Quadlet boot integration without competing lifecycle controllers. Profiles/replicas are later conveniences with safe graph/port/storage expansion, not scheduling.

## 8. Delivery phases and acceptance gates

The [ticket index](docs/design/TICKETS.md) is authoritative for prerequisite links; phases describe outcomes, not permission to skip dependencies.

The operator-approved [#19 foundation gate](docs/design/protocol-contract-19-proposal.md#dependency-gate), **`protocol-foundation-1`**, is **not implemented, shipped or passed**. It is limited to protocol discovery/check/codec behavior, with no IR, apply, target or provider capability. Implementation waits for verified #31/#32 and explicit main-orchestrator release; passage requires reviewed tests. Downstream contracts may use that passed gate instead of waiting for full #19 closure, allowing #38/#16/#10 to establish the secret-safe artifact prerequisites without a circular sequencing requirement. Other prerequisites and runtime safety gates remain mandatory. #19 stays OPEN until its full artifact, Pkl-free execution, target/outcome and Brine acceptance is met; foundation fixtures do not establish those features.

| Phase | Outcome | Exit gate |
|---|---|---|
| 0: correctness | Honest exits, pinned toolchain, audited engine behavior | Invalid config causes zero mutations; failures never masquerade as success |
| 1: contracts | Typed intent/bindings, protocol, policy, preflight, identity/locking | CLI and Brine share target and safety semantics; secrets stay references |
| 2: application relationships | Jobs, namespace lifecycle, storage, process/runtime requirements | Synthetic dashboard/VPN/job/worker fixtures work or fail clearly on both engines |
| 3: operational confidence | Prepared updates, recovery/backup evidence, recipe reuse | Failed preparation leaves old app running; isolated restore verified; independent packages compose |
| 4: optional expansion | Additional backends, boot integration, profiles/replicas | Capability-specific tests; no scheduler/host-manager scope creep |

Homelab rollout: isolated dashboard/exporter first, then selected simple applications, then stateful/auth/VPN/core monitoring after recovery tests. Preserve existing `hl` snapshots, no-prune intent, secret handling and verification. Do not perform production changes through this roadmap. Keep Watchtower/Autoheal labels; account for external mutations. Update Alloy Compose-label attribution to recognize Dill and replace Portainer-dependent dashboard widgets deliberately. Host config synchronization and Caddy remain external responsibilities.

## 9. Verification and implementer checklist

Run the design fixture checks:

```sh
PKL=/path/to/pkl-0.31.1 python3 docs/design/check_examples.py
```

They verify actual Pkl evaluation, typed constraints and serialized authoring results, **not** runtime support, graph validation, host access or safety guarantees. See [example notes](docs/design/examples/README.md).

For every implementation: update schema/normalization/hash/plan/apply together; add positive and negative examples; test both engines or reject a capability explicitly; assert zero mutation on preflight failures; test cancellation and ownership; update shared protocol fixtures for Brine. Use `make` targets with the required Go tags. Never use real secrets or production hosts in tests. Unsupported fields cannot be silently ignored. No claim is complete without commands, outcomes and stated verification gaps.
