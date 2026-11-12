# Alpha secret preview proof extension (approved design; not implemented)

Dill #84 and Brine #71 require more than comparing rendered plans: equal plans
cannot establish that the evaluated configuration, policy and relevant live
state are unchanged. This is a narrow private confirmation mechanism, not a
saved plan, transaction or new deployment protocol.

`dill --secret-target ID -p STACK --engine ENGINE --engine-socket /ABS/SOCKET plan [existing flags] --proof-key-file ABS --proof-file ABS` accepts both
flags together, only on a secret-enabled plan. On a successfully completed
plan, including the normal drift exit status 1, Dill atomically writes a fresh,
private mode-0600 file containing exactly:

```json
{"contract":"dill.alpha-secrets-preview/1","proof":"<base64url HMAC-SHA256>"}
```

A parse or operational error produces no proof and uses the secret path's fixed
safe error. Brine supplies a fresh output filename for each invocation, treating
exit 1 as a completed plan only if the new valid proof and valid plan are both
present. Neither path is included in the MAC. The key is exactly 32 random bytes
in a private Brine/operator-owned location outside editable or served project
files. Reject symlinks, unsafe ownership or permissions, nonregular files and
incorrect lengths. The output path must also be private and safe; use no-follow,
atomic mode-0600 creation and do not overwrite key, policy, config, source or
other preexisting files. Key rotation invalidates all previews.

The domain-separated HMAC covers a canonical length-delimited representation of
the effective evaluated configuration (including import semantics), the exact
candidate binding, authorized policy inputs, target/engine/socket, operation
selection and options, relevant live configuration, identity, ownership,
mounts, image IDs and resource facts, lifecycle state relevant to safety, and
the normalized plan. It never reads source bytes or emits an unkeyed value hash.
The exact live fields must be specified from the implemented planner and
ownership checks, independently audited and tested for stable repeats and
meaningful change invalidation. Exclude irrelevant raw inspect timestamps,
counters and health samples. Semantically identical re-evaluation may retain
the same proof; this is not a claim about byte-identical remote import provenance.

Brine stores proofs privately, never in API responses, UI, history or logs. It
serializes candidate edits, preview, confirmation, activation and deploy, repeats
the plan under that lock and constant-time compares proof, candidate identity and
checked plan status/output before activating exactly that candidate. `up` still
performs independent preflight. Trusted-operator quiescence remains required;
foreign writers, source replacement and mutable remote image tags are not frozen
by the proof. Paired golden fixtures and real-binary tests are required before
this interface is considered implemented.
