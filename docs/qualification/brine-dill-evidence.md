# Evidence template: Brine + Dill private HTTPS

Copy into a private run record; leave this template unchanged. Use one record per run and one case record per attempt (including failed or aborted attempts). See the [qualification checklist](brine-dill-private-https.md). **This blank template records no verified runtime results.**

## Planned run — fill before execution

| Field | Planned value |
| --- | --- |
| Run ID, operator/reviewer alias, UTC window and lab lease/expiry | — |
| Checklist revision; profile, required cases and optional extensions | — |
| Brine host and target host aliases; architecture, distro, kernel and Docker versions | — |
| Brine release URL, OCI index/platform digest and revision | — |
| Target Dill release(s), native asset name, expected SHA-256; Pkl version and SHA-256 | — |
| Brine and target daemon/socket aliases and independent binding verification method | — |
| Browser→Brine TLS/auth boundary and Brine→agent pinned HTTPS boundary | — |
| Private Brine data/registry, agent staging/catalog/coordination/journal identities and single owners (aliases) | — |
| Projects/stacks, fixture file digests, image digest(s), expected mount and marker digest | — |
| Source inventory/event capture, status GET interval, readiness/operation/recovery deadlines | — |
| B3/B4 idle baseline and recovery budget, target boot ID probe, boot-start policy, same-host/storage verification and out-of-band access | — |
| C1/C2/R1 fault boundary, observation method, exact disposable process IDs or roles, containment and abort conditions; separate reviewed trigger prerequisite for C3 | — |
| Upgrade order, before/after tuple, state to preserve, eligibility for U3 | — |
| Quarantine/disposition owner, expiry and independent reviewer | — |

## Observed environment and provenance — fill from actual checks

- Checked UTC, operator/reviewer aliases, evidence bundle reference: —
- Brine installed image/index/platform digest and revision; actual `brine --version`, bundled Dill and Pkl outputs: —
- Target installed Dill binary hash/version per phase; Pkl hash/version; release API digest comparison: —
- Actual native host/engine versions, endpoint/daemon correlations, isolation/ACL verification and listener/auth/pin/binding check outcomes (record match/mismatch, not secrets): —
- Verified persistent-state identities before/after, fixture/manifest and image hashes, initial baseline inventory, collector readiness and known gaps: —

## Observed case results — do not copy expected results here

| Case | Status | Observation or blocker (not expectation) | Evidence references |
| --- | --- | --- | --- |
| B1: cold boot, private HTTPS and read-only enrollment | NOT RUN | — | — |
| B2: historical shared-bearer write opt-in and review (not #141 acceptance) | NOT RUN | — | — |
| D1: managed create and application data check | NOT RUN | — | — |
| D2: workload crash and existing-volume update guard | NOT RUN | — | — |
| B3: populated target, idle agent-only restart | NOT RUN | — | — |
| B4: populated target, same-host OS reboot while idle | NOT RUN | — | — |
| C1: Brine process crash and persistent-state recovery | NOT RUN | — | — |
| C2: interrupted admitted Dill request and write block | NOT RUN | — | — |
| R1: lost apply response, same-request observation only | NOT RUN | — | — |
| U1: released Dill agent upgrade, prior status and data | NOT RUN | — | — |
| U2: checked stateless create/update after upgrade | NOT RUN | — | — |
| C3: target-host outage during an admitted update (separate) | BLOCKED | Parked: needs a reviewed disposable-host trigger and recovery path; not attempted. | — |
| U3: Brine image version upgrade (separate) | NOT RUN | — | — |
| G1: Git fence/deduplication (separate) | NOT RUN | — | — |

Use `PASS` only with all case predicates observed and correlated, `FAIL` for a contradicted predicate, `INCONCLUSIVE` for an attempted case without enough evidence to decide, `BLOCKED` for an unmet prerequisite, and `NOT RUN` for an unattempted case. Keep operation state/effects separate from case status: for C2, a witnessed `unknown / unknown` plus successful containment can satisfy the crash case without making the deployment successful. The agent-upgrade profile passes only if B1–B4, D1–D2, C1–C2, R1 and U1–U2 all pass. Report C3, U3 and G1 independently. Neither B3/B4 nor C2 verifies outage during an in-flight update, and historical B2 is not #141 acceptance.

### Per-case record — duplicate for each attempt

```text
Case ID / attempt / stack alias:
Planned hypothesis and exact success/failure criteria (or checklist ref):
UTC start/end and deadlines:
Actual commands/actions, arguments with secrets omitted, tool exit codes:
Observed Brine browser action/delivery and HTTP method/status (not raw request/response):
Request alias / optional Dill operation alias / agent & journal aliases:
Observed stage/manifest/selection correlation (digest or alias) and advertised mode:
UTC time series: evidence ref | request state/effects or unavailable | engine/app observation:
Fault or restart: scope, trigger, time, actual process/host/connection interrupted and proof:
B3/B4/C3: target boot ID before/after (restricted aliases), same-host/storage proof and Brine continuity:
B3/B4: agent and Docker process instances, workload StartedAt and live restart policy before/after:
B3/B4: original D1 status/request/operation and agent/journal identity before/after:
Inventory before/after: daemon, exact-ID matches, network, mounts/volumes, image and other changes:
Engine event window and gaps; expected and unexpected effects:
App marker before/after: expected/observed digest, reader exit, reseeded yes/no:
Count and type of submissions/status GETs; evidence of no replay or unplanned mutation:
Verdict and rationale (including uncertainty, blocker, or discrepancy):
Follow-up owner / containment / disposition:
```

Example of format only, **invented and not evidence**: `R1 | INCONCLUSIVE | confirmation response absent, but forwarding of the POST was not observed; one later status GET was unavailable | sample-r1`. Do not turn an unavailable result into `failed / none` or a successful retry.

## Evidence handling

- Record exact commands locally with secrets omitted and identify the observer and UTC timestamp. Capture bounded sanitized extracts or summaries; no raw headers, HAR/traces, state dumps, environment, pairing bundles, keys, passwords, cookies, CSRF tokens, private URLs/paths or log payloads. If safe, retain the exact request/operation/resource ID mapping and status link in the restricted operator record; use consistent aliases in shareable reports. A status link or ID is for observation, not authorization. Do not send the raw artifacts for redaction elsewhere.
- Correlate releases and fixture source → target/manifest/selection → request and disposition → independent engine and application observations. Record delivery, journal result, and engine effects separately; don't infer one from another. A green UI, HTTP status, Docker object name, or hash of the wrong object cannot substitute for a missing observation.
- Record repeat attempts separately and preserve the first failure or ambiguity; never overwrite an unfavorable result with a rerun. Keep incomplete or uncertain histories quarantined and preserve state. Before any independently authorized disposal, record exact resource scope and disposition; do not dispose of shared or unidentified resources.

## Run decision

- Agent-upgrade profile verdict and exact tuple: —
- C3 in-flight target-host outage, U3 Brine-image upgrade, and G1 Git recovery verdicts: —
- Failed, blocked, inconclusive or untested cases and why: —
- Checks run and not run; collection and redaction gaps: —
- Quarantined resources, disposition, owner and next decision date: —
- Reviewer/date and evidence references: —
