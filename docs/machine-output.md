# Machine-readable CLI output

This inventories the built-in `dill` CLI in this checkout, not a promise that
all output is a stable API. External `dill-<command>` plugins, including any
separately installed executables, define their own behavior. Discovery is
per-surface: do not infer support from the binary version, a different command,
or a future proposal. Command flags below follow the command name; global
flags generally precede it. Go flag parsing stops at the first positional
operand, so put command options before service names.

## Finite legacy JSON

Unless stated otherwise, select `--format json`; the default is text. A
successful encoding is one JSON value on stdout, normally newline terminated.
These are existing unversioned representations, not a common envelope.

| Built-in | Successful stdout JSON | Important details |
| --- | --- | --- |
| `version` | `{"version":"..."}` | Default is text. Selected-JSON usage errors produce `{error,kind}`. |
| `caps` | adapter capability object | JSON is the default; `--format json` is also accepted. `--engine` selects static Docker/Podman capabilities, not target readiness; see [capabilities](capabilities.md). |
| `config` | resolved configuration object | JSON is the default, `--format yaml` is also supported. JSON is indented and **has no appended newline**. It can contain sensitive configuration; protect stdout. |
| `images` | array of `{service,image}` | Sorted by service; empty array is `[]`. Desired configuration, not an image inventory. |
| `volumes` | array of volume name strings | Stack-owned volumes; empty array is `[]`. |
| `ps` | array of `{name,id,state,status,image,ports}` | Empty array is `[]`; `--all` adds missing configured services. A per-container lookup failure is represented by `state: "error"` and its message in `status`, not necessarily a failing exit. `ports` can be null. |
| `status` | `{stack,engine,config_file,services,containers}` | Container elements have the `ps` fields; see implementation for empty/null behavior. |
| `stacks`, `ls` | array of `{name,engine,containers,running,stopped}` | `ls` aliases `stacks`; empty array is `[]`. |
| `port` | array of published-port strings, or `null` | Optional container-port operand filters with string matching, not a structured port parser. |
| `doctor` | array of `{name,ok,detail?}` | Detail may contain paths or diagnostics. Exit 1 if any check fails, even though JSON was successfully rendered. |
| `validate` | `{"valid":true}` or `{valid:false,error,kind}` | `kind` is `invalid_input`, `evaluator_failure`, or `operational_failure`; error text can be sensitive. `--stdin` reads Pkl via a temporary file. |
| `plan`, `diff` | `{changes,summary}` | `changes` is an array of `{service,kind,diffs}`; kind is `create`, `recreate`, `remove`, or `noop`; diffs may be null or an array of `{field,before,after}`. Summary has numeric `create`, `recreate`, `remove`, `noop`. Before/after can contain sensitive configuration. Selected-JSON failures use `{error,kind}` as for validate. `plan` exits 1 for drift; `diff` exits 0 for drift. |

Legacy success JSON does **not** imply JSON errors. For `caps`, `config`,
`images`, `volumes`, `ps`, `status`, `stacks`/`ls`, `port`, and `doctor`,
errors generally leave stdout empty and print diagnostics to stderr. Even
`plan`, `diff`, `validate`, and `version` only produce their failure object
once their local parser has actually selected JSON: a global parse/env-file
failure, or a local parse failure before `--format json`, can happen first.
Flag parser diagnostics and the normal top-level error may both appear on
stderr, including alongside a JSON failure. Output delivery failures can leave
partial output; do not assume a parseable terminal document. Secret-enabled
invocations have special fixed diagnostics; do not treat legacy JSON as a
general secret-safe transport.

Normally exit 0 means success, 2 means usage or decoded invalid input, and 1
means operational/evaluator failure. Drift (`plan`) and failed checks
(`doctor`) are the important result-with-exit-1 exceptions. Nonzero is not a
reliable distinction between drift and failure without checking the result.
A child engine command's exit status is not passed through verbatim by these
legacy wrappers: a child error is generally exit 1.

## Explicitly versioned or specialized surfaces

* `validate --format operation-v1`: one `dill.validate/1` object after argument
  parsing and stdin setup, with `version`, `operation`, `outcome`,
  `engineEffects`, and `stack`; errors still go to stderr and earlier failures
  may produce no object. It is offline validation, not a deployment guarantee.
  See [validation result](validation-operation-v1.md).
* `operation-capabilities [--format json]`: static JSON discovery, default
  JSON; invalid arguments exit 2 without a JSON result. `operation <action>
  --contract dill.operation/1 --format json ...` returns a bounded, redacted
  terminal result with independent outcome/effects, not a stream. It can prompt
  on stderr for attended destructive actions. Its `plan`/`diff` drift exit
  distinction persists. Missing/incomplete output means unknown, not success.
  See [operation contract](operation-v1.md) for selectors, supported actions,
  error codes, safety, and qualification boundaries.
* `preflight-capabilities [--format json]` provides static versioned discovery;
  `preflight <action> --contract dill.preflight/1 --format json ...` produces
  a bounded review for a restricted qualified host. It is not a reservation
  or authorization and may write coordination metadata. See
  [named preflight](named-preflight-v1.md). Unsupported discovery arguments
  can exit 2 without a result.
* `coordination inspect` and `coordination acknowledge` produce one JSON object
  `{version,record,error}` on stdout, including fixed-message argument and
  operational failures (exits 2 and 1 respectively); success exits 0. They
  inspect or acknowledge offline state, not replay or repair it. Acknowledge
  changes local metadata and requires explicit inspection attestation. See
  [coordination design](local-mutation-coordination-design.md).
* Strict-secret operation is disabled. `secret-capabilities` fails without
  advertising a capability; `secrets check` emits the fixed unavailable JSON
  and exits 1. Secret selectors and proof flags fail with the fixed secret error.
  See [disabled features](disabled-alpha-secrets.md).
* `protocol` and `protocol run` are a separate experimental bounded transport,
  not an execution API. `protocol` emits one newline-terminated discovery JSON
  record; `protocol run` reads one bounded JSON request through EOF and emits
  NDJSON records. Discovery/check advertise only `protocol.check`; see the
  [protocol proposal](design/protocol-contract-19-proposal.md) and its fixtures.

## Non-JSON, interactive and streaming commands

| Built-in | Current behavior |
| --- | --- |
| `up` (also the default with no command), `down`, `stop`, `start`, `restart`, `pull`, `wait` | Human progress, typically stdout; `--quiet` suppresses applicable progress, not a JSON mode. `--dry-run` emits human descriptions where supported. `down` does not prompt. |
| `rm`, `teardown` | Human progress/stdout and destructive confirmation on stderr with input from stdin; `--yes` skips confirmation, `--dry-run` avoids it. Declining normally exits 0, not a machine success assertion. |
| `logs` | Raw container log bytes on stdout; `--follow` can run indefinitely. One container is unprefixed; multiple containers are streamed concurrently with line prefixes unless `--no-prefix`; TTY prefixes may be colored. Individual multi-container errors are warnings and do not necessarily cause a nonzero exit. No JSON/NDJSON log event format. |
| `exec` | Inherits stdin/stdout/stderr from the container process via engine CLI; interactive TTY when stdin is a terminal. No JSON wrapper. |
| `top`, `cp` | Delegate to the engine CLI; stdout/stderr are engine-defined; `cp` copies raw files, not JSON. |
| `fmt` | In-place formatting, or `--stdin` reads raw Pkl and writes formatted raw Pkl to stdout; Pkl diagnostics go to stderr. |
| `init` | Creates a starter Pkl file and prints a human confirmation, or a dry-run description. |

Pulls and `up` use terminal-dependent progress: non-TTY output reports the
start and discards incremental pull progress, whereas TTY output may use cursor
control and may suppress concurrent updates. Neither is an event protocol.
Help/unknown-command and flag diagnostics are human text on stderr; no-command
means `up`, not discovery. Pkl/evaluator, logging verbosity, engine subprocesses,
and failures can add diagnostics. Do not merge stderr into stdout before parsing
JSON; do not parse human progress as a stable interface.

## Gaps / future work, not implemented contracts

There is no universal versioned envelope for legacy commands, no structured
JSON logs/progress or replayable event stream, no generic machine interface for
`exec`, `top`, `cp`, or raw Pkl output, and no blanket redaction guarantee for
legacy JSON. Cross-command structured errors, reliable output delivery,
stream framing/backpressure, explicit capability negotiation for each format,
and security review are future design work, not current features. Prefer the
specific versioned discovery and contracts above when their scope fits; never
silently fall back from a required machine contract to human output.
