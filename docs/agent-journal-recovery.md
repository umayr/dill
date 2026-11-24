# Inspect recovery evidence for one deployment request

If a checked deployment's result was lost or is uncertain, inspect the stopped
host agent's **original** deployment journal locally. Use the exact directory
configured as `--https-apply-state`, the `X-Dill-Journal-Id` saved with the original
submission, and that submission's `requestId`. Both IDs are 32 lowercase hex
characters. Do not substitute an ID taken from the journal you are attempting
to verify.

```sh
dill agent journal-recovery --state-dir /absolute/private/operations \
  --journal-id "$journal_id" --request-id "$request_id"
```

The standalone form is `dill-agent journal-recovery` with the same flags. Run
as the state owner with the agent stopped. The same local, private-file and
locking requirements as [journal status](#journal-status-and-capacity) apply. The
command takes no bearer token or other credential, does not contact a host,
engine, or API, and does not initialize, upgrade, compact, or write the journal.
An ordinary read may update access time.

## Interpret the result

Exit `0` emits one JSON object (`version: "dill.agent-journal-recovery/1"`) with
`journalId`, `requestId`, `state`, and `effects`. If a row is present, the output
also includes its exact `recordedState`, its `mode` (`create`, `update`, `remove`,
or `fence`), and its `operationId` if recorded. It does not emit paths, target
or stack names, stage or manifest identifiers, or resource records.

| `state` | `effects` | Meaning |
| --- | --- | --- |
| `succeeded` | `complete` | The journal recorded successful completion. This does not verify current resources. |
| `failed` | `none` | The journal explicitly recorded failure with no effects for this request. |
| `failed` | `partial` | The journal recorded partial effects. It does not establish which changes remain. |
| `not-admitted` | `none` | An existing durable fence explicitly recorded non-admission. Inspection never creates a fence. |
| `unknown` | `unknown` | The request is recorded as `unknown` or still `pending` on disk. `pending` does not mean running or effect-free. |
| `missing` | `unknown` | No row matches in the inspected, ID-matched journal. Absence is not a fence or evidence of no effects. |

A missing file, busy or unsafe directory, invalid or unsupported journal,
legacy journal without an ID, or mismatched journal ID exits `1` with a fixed
stderr diagnostic and no result. Treat missing or incomplete stdout as no
result. A matching ID is not proof of freshness: this command cannot distinguish
an older copy of the same journal or verify the target or effects against an
engine. Use the original state directory with trusted, stable ancestors; do not
restore an older copy to recover a write.

Preserve the journal and related state when effects are uncertain. No result
repairs or replays a request, acknowledges an unknown outcome, clears blockers,
or authorizes resuming writes.

## Journal status and capacity

Inspect an **offline** host-agent deployment journal using the exact directory
configured as `--https-apply-state`:

```sh
dill agent journal-status --state-dir /absolute/private/operations
```

The command also works as `dill-agent journal-status` when using the standalone
binary to inspect state created by the combined agent. Run it as the owner of the
state directory. It takes no credentials, does not start a listener, and does not
contact Docker, Pkl, or the deployment API. It reads only `operations.json` in the
specified existing directory. The directory and file must be owned by the caller
and private; neither final path component may be a symlink. Use trusted, stable
ancestors and a local filesystem with working `flock` semantics.

The command takes a nonblocking shared lock on the existing directory. It fails
if the agent holds its exclusive lock, rather than reading active state or
waiting. It creates no lock file and does not initialize, sync, compact, or
upgrade the journal, including when reporting on a supported legacy journal.
An ordinary file read may update access time. `X-Dill-Journal-Capacity: low` on
existing deployment responses is the separate online warning; this command does
not add an online endpoint.

### Reading the result

Success exits `0` and prints one JSON object followed by a newline. Missing,
unsafe, busy, corrupt, or unsupported state exits `1` with a fixed diagnostic on
stderr and no summary. If stdout delivery fails, the command exits `1`; treat
missing or incomplete JSON as no result. `--help` prints usage on stderr and
exits `0` without inspecting state.

| Field | Meaning |
| --- | --- |
| `version` | Output schema: `dill.agent-journal-status/1`. |
| `journalVersion` | On-disk format: `1` (legacy) or `2`. |
| `bytesUsed`, `byteLimit` | Exact bytes read and the configured 16 MiB journal size limit. |
| `lowCapacity` | True at `floor(byteLimit * 9 / 10)` bytes or above, matching the live warning threshold. |
| `admissionByteCeiling` | The journal size limit less the writer's `outcomeReserveBytes`; admission and fencing must stay within this ceiling after appending a record. |
| `admissionHeadroomBytes` | Bytes between the current size and the admission ceiling, clamped at zero. This is not an estimate of remaining requests. |
| `records` | Total retained records, including compacted results and fences. |
| `notAdmitted`, `succeeded` | Durable fences and successful operations, respectively. |
| `failedNoEffects`, `failedPartial` | Failed records with `none` or `partial` recorded effects, respectively. |
| `unresolved` | Records persisted as `pending` or `unknown`. An offline `pending` record does not mean an operation is still running. |

Only aggregate counts and limits are emitted: no journal, request, operation,
stack, target, snapshot, or resource identities. The command checks bounded
canonical encoding and journal record/ownership consistency. It does not verify
that the journal matches a configured target or that recorded results match the
engine. It cannot see in-memory errors, check free disk space for an atomic
replacement, predict the size of a future record, or account for growth from a
future legacy upgrade. Neither exit `0` nor positive headroom means writes can
safely resume. Partial and unresolved effects may block further operations on
the affected stack independently of capacity.

Preserve the journal and related deployment state if admission fails, capacity
is low, or effects are uncertain. Do not delete, truncate, replace, or roll back
the journal to regain capacity or clear a result. This command offers no repair,
recovery, or override.
