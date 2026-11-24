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
locking requirements as [journal status](agent-journal-status.md) apply. The
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
