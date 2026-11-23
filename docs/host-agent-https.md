# Direct HTTPS host agent (experimental, read-only by default)

Run one agent per Docker target. The standard `dill` binary includes `dill agent`, which accepts the same arguments as the standalone `dill-agent` shown below. This mode is an alternative to the private Unix agent listener. The default HTTPS routes serve only session, inventory, inspect and logs. Staging, preview, deployment, removal, manifest and operation status require host-local opt-ins, explicit operation grants and a separate deploy bearer. Environment reveal is not available over HTTPS. Docker still uses an explicitly enrolled local Unix socket. Treat log and inspection output as potentially sensitive.

1. As the service account, run `dill-agent init-https --dir /absolute/new/credentials` under a trusted parent. This creates an owner-only directory with a TLS certificate/key, session signing seed, and unique bearer credential. Existing directories are never changed. If creation is interrupted, inspect the partial directory rather than reusing it blindly.
2. Start the agent with `--https-dir /absolute/new/credentials --https-listen <private-ip>:<port>` and the normal `--engine-socket`, `--engine-id`, `--daemon-id` and `--generation` enrollment flags. Supply a literal private address; apply host firewall rules for your network and keep the credential directory and all ancestors protected. Do not run a public listener or forward the Docker socket.
3. From a private local terminal, invoke the same command with `--print-https-pairing` appended. This prints one JSON bundle to stdout and exits without listening; **the output is a reusable secret**, not an expiring code. Copy it only through trusted local means, with the listener address entered separately in Brine. Do not log, paste into public support messages, or store the bundle in ordinary temporary files. The export records the configured engine binding; the signed session checks the actual daemon before Brine accepts it. The exact v1 bundle remains monitor-only, including when deployment is configured. The deploy credential is never part of this export.

## Separate deployment authority

Provision an independent random 32-byte deploy credential out of band. Never repurpose a current or retired monitor token as a deploy token. Store it as unpadded base64url (optionally followed by one newline) in an owner-only regular file under trusted ancestors. Pass its absolute path with `--https-deploy-token-file`. Staging and deployment startup configurations require this file and reject a credential equal to the monitor token. `init-https` does not create it. Grant each operation with a separate `--https-deploy-grant NAME`; the default is no grants, and unknown names are rejected. A grant does not enable or replace the corresponding backend configuration or its checks.

| Credential | Grant | HTTPS method and route |
| --- | --- | --- |
| Monitor | built-in monitor | `POST /v1/session`, `/v1/inventory`, `/v1/inspect`, `/v1/logs` |
| Deploy | `stage` | `POST /v1/snapshots` |
| Deploy | `manifest` | `GET /v1/snapshots/{id}` |
| Deploy | `preview` | `POST /v1/deploy/preview` |
| Deploy | `apply` | `POST /v1/deploy/apply` |
| Deploy | `journal-apply` | `POST /v1/deploy/journal-apply` |
| Deploy | `remove-preview` | `POST /v1/deploy/remove-preview` |
| Deploy | `remove` | `POST /v1/deploy/remove` |
| Deploy | `status` | `GET /v1/deploy/operations/{requestId}` |
| Deploy | `fence` | `POST /v1/deploy/fence` |

Clients send exactly one `Authorization: Bearer` header using the designated credential. Neither credential falls back to the other. In particular, status does not grant manifest access, fencing or any other operation. These names configure a local policy; there is no new bundle or session format. A client that sends the old monitor bearer for deployment requests cannot deploy through this listener.

At the HTTPS authorization boundary:

- `401 Unauthorized`: TLS or a single valid bearer is missing (including wrong or revoked credentials).
- `403 Forbidden` with a plain-text `forbidden` body: the bearer is valid but lacks authority for this configured route. Do not retry with a different credential or escalate. This is distinct from the existing signed monitor protocol's structured stale-epoch error.
- `404 Not Found` after authentication: the method/path is not exposed or its backend is unavailable. An authorized backend can separately return `404` for missing data; this never proves absence of deployment effects.

## Optional catalogued staging

Both `--https-staging-root` and `--https-staging-catalog` must point to distinct existing private roots. With the deploy credential configured, `--https-deploy-grant stage` enables uploads and `--https-deploy-grant manifest` independently enables lookup. Without the staging flags the routes are unavailable. Staging can store sensitive files, so enable it only when the Brine instance and roots are trusted. It uses the bounded [snapshot staging protocol](snapshot-staging.md) but does not expose `dill-serve` health/status. An ambiguous upload is not safe to retry automatically. Staging alone never evaluates the project, writes to Docker, or deploys it. The [staging admission budget](snapshot-admission-budget.md) limits logical file bytes and stage count per root, but does not guarantee physical free space or automatically remove stages.

## Optional deployment preview (native Linux Docker)

With the combined `dill agent` entrypoint, supply `--https-preview-state` (the same private coordination directory used by the qualified CLI) and `--https-preview-pkl` (an absolute operator-managed Pkl executable), in addition to the staging configuration and `--https-deploy-grant preview`. Only enable this for **trusted projects and a trusted Brine instance**: preview evaluates Pkl with the agent account's privileges. This is not a sandbox. Do not expose deployment preview from a network where the bearer credential is not adequately protected.

Authenticated `POST /v1/deploy/preview` accepts exactly `stageId`, `manifestSha256` (the catalog's lowercase hex digest) and `stack`. The agent revalidates the published tree and runs its own binary's `dill preflight up` with the configured engine socket and daemon ID, shared coordination state, no-prune and already-present images (`pull never`). Client-supplied command, paths and overrides are not accepted. The successful response is the existing `dill.preflight/1` result. This preview may write coordination metadata but does not mutate Docker. It is not an approval. When checked deployment is enabled and permitted by the recorded ownership, the response advertises `X-Dill-Deployment-Mode: create` or `update`.

## Optional checked deployment, removal and status

Configure `--https-apply-state /absolute/private/operations` on the combined agent to make the journal-backed routes available for explicit grants. It must be a separate existing owner-only directory, with trusted stable ancestors. Keep it, the staging roots, and the shared CLI coordination state durable. Never remove or recreate state to clear an uncertain outcome. One agent process owns the history directory. The journal retains every request ID and result. It compacts superseded snapshot references and resource IDs without dropping live ownership, removal tombstones or unresolved intent. Existing compatible histories upgrade in place; do not downgrade or replace the upgraded journal with an older copy. At 90% of the 16 MiB journal limit, deployment responses advertise `X-Dill-Journal-Capacity: low`; admission and fencing fail closed with HTTP 507 when space is exhausted. Existing status remains readable. Keep disk headroom for the atomic replacement and do not delete history to make room. The earlier experimental create-only history did not record exact ownership and cannot be used with this format; it is not an adoption path.

`POST /v1/deploy/apply` accepts the exact preview's `stageId`, `manifestSha256`, `stack`, and `selectionDigest`, a 32-character lowercase hexadecimal `requestId`, and `confirm: "create"` or `"update"` as advertised by the preview. The agent checks the snapshot and preview, saves intent, and runs checked `dill operation up` with the same coordination state as cooperating CLI callers. Creation requires all selected resources to be absent. An update requires exact recorded immutable container and network IDs. It may add containers but cannot omit managed containers or attach existing volumes (Docker volume names can be reused). No operation falls back to the legacy executor or adopts resources.

`POST /v1/deploy/remove-preview` accepts `stack`, uses the last successfully deployed stage, and returns a qualified `down` preview plus `X-Dill-Prior-Request`. `POST /v1/deploy/remove` accepts `stack`, `selectionDigest`, a new `requestId`, `priorRequestId` from that header, and `confirm: "remove"`. Removal only stops and deletes the matched containers. It retains volumes, the network, images, and snapshots. A removed stack is recorded, not silently recreated or adopted. The deploy bearer can submit only explicitly granted operations; a deploy credential is not itself an attended or Git approval.

`GET /v1/deploy/operations/{requestId}` observes a request without submitting it. A known result contains the request ID, stack, state, effects, and when available a validated Dill operation ID. A disconnect after durable admission does not request cancellation. Pending at process restart is `unknown`, never replayed; unknown or partial effects block further writes for that stack. `404` or a delivery failure does not prove absence of effects. Use [local recovery inspection](agent-journal-recovery.md) to check what the stopped agent's original journal recorded for one request. There is no automatic acknowledgement, retry, deletion or rollback. Direct engine access or an uncoordinated CLI is outside the cooperative locking guarantee.

### Fence a request that was never admitted

For recoverable submissions, capture `X-Dill-Journal-Id` from the managed preview, then send the same header on `POST /v1/deploy/journal-apply` (same body as apply), status lookups, and fencing. The journal-bound apply route rejects absent or mismatched IDs. Keep the ID with the pending request. A new journal gets a different ID; never restore an older copy of a live journal as a way to recover a write.

An authorized client may explicitly `POST /v1/deploy/fence` with the original journal header and exactly `requestId`, `stack`, and `confirm: "fence-if-unadmitted"`. If the request already exists, Dill returns its status without changing it. Otherwise Dill durably records `state: "not-admitted", effects: "none"` before responding. The same request ID can never subsequently be admitted, including an apply that already entered preflight. Fencing never cancels or rolls back an admitted operation. A missing status, failed fence, or mismatched journal is not proof that no write occurred. Each fence consumes a small permanent journal record.

## Credential lifecycle

TLS uses an independently shared SHA-256 pin of the leaf certificate. The signed monitor protocol still uses its own persistent signing key. To invalidate a bearer, replace its token file through an authorized host operation and restart; the other token is unaffected. Removing the target from Brine alone does not invalidate a copied monitor bearer. Re-pair monitor clients separately. Deploy credential replacement does not rotate the journal, add an authority generation to confirmations, or establish that an existing pending request may safely resume. The generated certificate is valid for five years; renewing it changes the pin and requires re-pairing. Protect the daemon socket separately; the agent's network credentials are not Docker credentials.
