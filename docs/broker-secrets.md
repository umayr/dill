# Broker-delivered static secrets

This contract is published in Dill v0.12.0 and Brine v0.5.0. Provider authentication and resolution belong to Brine, never Dill agents. It is separate from retired Alpha secret execution; never use ordinary or retired execution as fallback.

## Boundaries and profile

`broker-requirements --contract dill.broker-secrets/1 -f /absolute/captured/compose.pkl -p stack` discovers value-free logical names, per-service file grants, and canonical evaluated-config digest from trusted captured Pkl. It is not sandboxing, target qualification, resolution, approval, or mutation. Verify and retain the complete source capture and target enrollment independently.

`dill.broker-profile/1` is a private operator file (canonical no-symlink path, regular private 0400/0600, namespace UID0-owned, at most 64 KiB). It admits exactly one Docker or Podman qualification and rejects unknown/duplicate fields, ambiguous engines, cross-engine layouts and invalid maps.

`broker-profile-info --contract dill.broker-profile/1 --broker-profile /absolute/private/profile.json` is an offline exception: exports engine/target/endpoint commitments and `runtimeOutcome:not-run`; it needs neither runtime store nor engine and proves no health. Non-init broker commands may use `--broker-profile` instead of `--engine-socket` and `--coordination-daemon-id`, never both. Broker-local also requires explicit engine-kind, target-digest and endpoint-digest pins. HTTPS profile opt-in is Docker-only and must match existing monitor socket/daemon enrollment; it does not enable remote Podman.

For HTTPS enrollment, credentials and route grants, see the [host agent guide](host-agent-https.md). Brine's [operator setup](https://github.com/umayr/brine/blob/v0.5.0/docs/broker-operator-policy.md) covers acquisition and application policy.

## Local commands and recovery

`broker-init`, `broker-prepare`, `broker-preflight`, `broker-managed-operation`, and `broker-retirement-prepare` require `--contract dill.broker-secrets/1 --broker-store /absolute/private/store`. Init explicitly provisions; missing used state is never recreated. Non-init additionally needs trusted source, coordination and profile-or-legacy target inputs. In legacy Docker mode these include `-f`, `-p`, `--engine-socket`, `--coordination-daemon-id`, `--coordination-state-dir`, and `--source-digest`; profile mode replaces only socket/daemon selection. Prepare accepts bounded private stdin; review accepts scope/handle metadata; managed execution also needs selectionDigest and native ownership expectation. It rechecks capture and ownership under target lease, activates before mutation, then uses captured DAG and journal. Values never appear in replies.

The native broker commands above that perform operations require the runtime store. `broker-requirements`, `broker-profile-info`, and local `status`/`fence` are distinct exceptions; status/fence need no runtime material.

`dill broker-local ACTION` supports init, journal, stage, snapshot, requirements, prepare, retire, review, apply, status, fence. It requires explicit contract, target selection (socket/daemon or profile plus pins), coordination, history, staging/catalog and Pkl flags; init requires existing store and creates a new history directory. Other actions require existing operations state. Busy/lost/uncertain outcomes are unavailable or unknown, never permission to replay. First broker admission upgrades journal to v3; preserve it, since older binaries fail closed. Any broker intent blocks ordinary preview/apply/removal for that stack.

Reviewed retirement uses `dill.broker-retirement/1`, exact predecessor references and an authenticated separately MAC-bound empty generation. It preserves prior generations, volumes and provider values. Exact empty evaluated config and full current stack coverage are required; omitted/duplicate workloads fail review and guardrails. No prune: omitted secret users block retirement and must be resolved through retained-source mount removal or separately reviewed lifecycle. Preparation alone does not mutate; normal review/apply/status and durable admission follow. Original-request reconciliation uses the same request ID after uncertain/lost response; never submit a new request. Retain history and resources on unknown outcomes.

## Ownership and limits

Linux private generations are immutable per-service, read-only, target-checked for owner/mode, and length-framed HMAC authenticated; prepared generations expire after five minutes. Activate durably before engine mutation and retain active material on uncertainty; no automatic deletion. Namespace profiles require mapped grant owners, namespace UID0 private-root ownership, and executor/engine matching user AND mount namespaces and identical root view. No automatic namespace entry, privilege helper, host-root remapping or non-Linux fallback.

Stable runtime paths are canonical no-symlink descendants of the authenticated private generation root; type, mode, single-link ownership and mapped IDs are checked against namespace/root snapshot before and after observation. Only default no-child-remapping container namespace is admitted; custom maps and unknown evidence are denied. Podman uses explicit private IPC. Broker markers alone never authorize adoption; authenticated active generation and exact read-only mounts are required.

## Measured runtime evidence

Paired development worktrees passed on disposable Debian 13/Linux 6.12.118 amd64, Pkl 0.31.1, Docker 26.1.5, Podman 5.4.2, RootlessKit 2.0.2. Every listed case passed real container-byte consumption, unauthorized UID and read-only denial, history non-disclosure, real update with deliberately lost successful response, no replay, app restart/original-request reconciliation, and reviewed empty-generation retirement (exit 0):

- Rootful Docker: file/local, Vault 1.20.4 KVv2/local, file/HTTPS, Vault/HTTPS.
- Rootful Podman: file/local.
- Rootless Podman: file/local in explicit private-root arrangement.
- Rootless Docker: file/local and file/HTTPS, retaining independent full signed Docker monitor enrollment.

Brine production Settings/Variables/native Plan/confirmation/shared History browser path passed real file acquisition and Docker delivery with independent byte verification and mobile/dark plus desktop/light evidence; fixture used mutual TLS and production session auth, not standalone dashboard/login. Infisical 0.167.16 separately passed fresh-sandbox Settings/Variables/Plan/confirmation/History, byte verification and provider viewer-write denial (403); its full recovery/retirement matrix is not claimed.

These are scoped results, not exhaustive provider/profile/transport cross-product: Vault on Podman/rootless and real Git/Restore browser runtime are not separately claimed. Remote Podman is unsupported on Docker-only HTTPS. Preserve failed/unknown outcomes and historical resources. These results are not published-release qualification.
