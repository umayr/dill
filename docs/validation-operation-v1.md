# Experimental validation result v1

`dill validate --format operation-v1` explicitly selects a versioned, offline
validation result. This is a narrow CLI output format, **not** an extension of
`dill protocol` and not a general execution API. The metadata-only protocol
continues to advertise only `protocol.check`. Unsupported formats are rejected
before config evaluation; there is no machine-readable capability negotiation
for this format yet.

Once arguments and any stdin temporary-file setup succeed, stdout contains one
JSON object with `version` (`dill.validate/1`), `operation` (`validate`),
`outcome` (`succeeded`, `invalid_input`, `failed`, or `cancelled`),
`engineEffects` (`none`), and `stack` (resolved stack name on success, empty
otherwise). Diagnostics and ordinary CLI errors go to stderr. The command
retains the existing validate exit codes. A missing or incomplete result is
not success. The format intentionally contains no raw evaluator error, which
may include configuration data.

Validation reuses the existing Pkl and runtime checks and does not connect to
or mutate an engine. `engineEffects` says nothing about the effects of evaluating
Pkl imports, acquiring Pkl, or creating the temporary file used by `--stdin`.
It does not establish that a deployment can succeed, check a live engine for
foreign names, or select an engine target. Plan, diff, lifecycle execution,
resource identities, execution capability negotiation, and shared Brine
fixtures are not implemented by this format. Existing plan/diff semantics are
unchanged.
