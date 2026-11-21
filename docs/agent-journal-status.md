# Read-only deployment journal status

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

## Reading the result

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
