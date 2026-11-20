# Storage identity and isolated restore evidence (#39 / #44)

This is a design aid, not an implemented backup or restore workflow. The rows
below are invented evidence, not results from a running engine or application.
No commands here read, copy, or restore data.

## Current inventory

- The current schema supports bind, named-volume, and tmpfs mounts; offline
  validation checks mount shape, not host mount identity or data safety. See
  [validation acceptance](validation-acceptance.md) and
  [capabilities](../capabilities.md).
- The narrow `dill.preflight/1` Docker profile inspects declared attached named
  volumes as part of a read-only engine review. Preview does write coordination
  metadata. Its name/settings evidence does not establish volume contents or
  distinguish identical same-name replacements; detached volumes and bind
  mounts are outside that profile. See [named preflight](../named-preflight-v1.md)
  for qualification and race limitations.
- Lifecycle volume removal currently performs a read-only retention check and
  retains existing volumes; this is not a backup or recovery guarantee. See
  [lifecycle failures](../lifecycle-failures.md).
- Typed storage bindings and target-side storage preconditions are future work
  (#39); application-consistent backup and isolated restore verification are
  future work (#44). Design examples are not runtime support.

## Synthetic evidence exercise

Imagine an application `catalog` with logical storage `catalog-data`. A future
review could bind that logical name to a target and storage identity; a future
restore exercise would need to establish its own destination and application
checks. The labels below are placeholders, not engine IDs or a proposed schema.

| Case | Source evidence | Destination evidence | Verification evidence | Interpretation |
| --- | --- | --- | --- | --- |
| Baseline | `source-A`, backup `sample-1`, consistency unknown | `test-B`, isolated and distinct from `source-A` | Not run | Not restore evidence, even if a backup is reported. |
| Positive example | `source-A`, `sample-1`, application-consistent capture attested | `test-B`, independently verified isolated and distinct | Restore completed; application-specific integrity check passed against `sample-1` | Hypothetical isolated restore evidence for that sample only. |
| Identity mismatch | Evidence expects `test-B` | Observed `source-A` or identity unknown | Not run | Reject/stop: never infer isolation from a different display name. |
| Wrong sample | `sample-1` expected | `test-B`, isolated | Check refers to `sample-2` | Inconclusive; the check is not bound to the intended backup. |
| Failed check | `sample-1` expected | `test-B`, isolated | Restore or integrity check failed or is unknown | No successful restore evidence; retain the failure for inspection. |

For actual future evidence, capture the target and storage identities and how
isolation was established, backup identity and capture consistency, restore
outcome, application-specific check and outcome, and time/context. Bind the
check to the restored backup and destination; treat missing, stale, ambiguous,
or conflicting evidence as unknown rather than success. A name or settings
hash alone is not proof of distinct underlying storage. Protect evidence from
secrets and sensitive paths; this exercise defines neither an evidence format
nor an implementation, authorization policy, retention policy, or procedure.
