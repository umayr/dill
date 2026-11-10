# Dill protocol foundation

**Status: implemented under review on the protocol branch; not a passed dependency gate or release.** The main orchestrator released implementation after #31/#32 verification; those broader tickets retain their accepted limitations. Gate passage still requires main-orchestrator verification and CI. [#19](https://github.com/umayr/dill/issues/19) remains OPEN until its full acceptance criteria are met.

## Dependency gate

The named **`protocol-foundation-1`** gate separates protocol plumbing from deployable artifacts. Once implemented, tested and reviewed, downstream contracts may depend on this gate without waiting for all of #19. Approval of this document alone does not pass the gate. Other ticket prerequisites still apply; a codec is not a runtime capability.

Foundation scope is discovery, one metadata-only check, and shared envelope codecs/fixtures. It establishes exact compatibility, bounded framing, safe errors and independent outcome axes. It provides **no IR, artifact render/validation/apply, target discovery, engine/provider access, secret resolution, locking, persistence or durable control**. `dill caps` keeps its existing static adapter-discovery name, output and semantics.

Full #19 still requires secret-safe normalized artifacts, Pkl-free semantic validation/application with ownership/policy/live checks, target identity, runtime outcomes and Brine integration evidence. The foundation enables #38/#16 contracts and their later #10 integration; it does not certify them or require their runtime features first. See the [ticket sequencing rules](TICKETS.md#19-staged-dependency-gate).

## Commands and compatibility

- **`dill protocol`**: discovery; emits one bootstrap discovery record plus newline, exits 0 and does not read stdin.
- **`dill protocol run`**: reads one complete bounded JSON request from stdin through EOF, then emits NDJSON records. One subprocess handles one request; there is no session or fallback to legacy CLI parsing.
- No other subcommand, operand or flag is accepted in this foundation, including legacy config/engine/env options. Invalid invocation produces the fixed bootstrap `INVALID_ARGUMENTS` result, never an argument echo or parser-generated usage diagnostic. Protocol dispatch must precede legacy env-file/config/Pkl/engine setup and input-bearing logging.

The fixed bootstrap identifier is **`dill.bootstrap/1`**. The sole experimental wire identifier is **`dill.dev/1`**. These are independent of binary versions and future IR/hash versions. Exact identifiers are required: no ranges, downgrade, implicit default, extension fields or unknown-event tolerance. A client must select the wire identifier in every run request even after discovery. Incompatible schema/semantics require a different experimental wire identifier; bootstrap evolution must retain useful fixed incompatibility errors for this bootstrap version.

Discovery has exactly this shape; empty capability lists are intentional, not wildcard support:

```json
{"bootstrapVersion":"dill.bootstrap/1","recordType":"discovery","protocolVersions":["dill.dev/1"],"operations":["protocol.check"],"features":["ndjson","resultAxes"],"irVersions":[],"targetCapabilities":[],"providerCapabilities":[]}
```

`features` are protocol features only. This record contains no target, artifact digest, provider registration, executable path, caller data or dynamic environment-derived metadata.

## Request and parser limits

The only accepted request shape is:

```json
{"protocolVersion":"dill.dev/1","operation":"protocol.check","requiredFeatures":[]}
```

All three properties are required, exact-case and non-null. The first two are strings; `requiredFeatures` is an array of unique strings, each exactly matching an advertised feature. The check accepts any subset of the advertised features and confirms only protocol agreement. There are no request IDs, caller-chosen operation IDs, payload, target, source, artifact, timeout, environment, options or extension properties.

Implementation limits for this foundation:

| Boundary | Rule |
| --- | --- |
| Input bytes | At most **4,096 bytes**, including whitespace; reject as soon as byte 4,097 is observed. Never accumulate an unbounded stream. |
| JSON nesting | At most **4 container levels**, counting the root object as level 1. Check during parsing, before building unbounded nested values. Schema validation remains stricter than this limit. |
| Strings and features | Decoded property names/string values at most **64 UTF-8 bytes**; at most **8** required features, with no duplicates. |
| JSON representation | One UTF-8 JSON object followed only by JSON whitespace and EOF. Reject BOM, invalid UTF-8, unpaired Unicode surrogate escapes, comments, trailing values, malformed/incomplete JSON and duplicate keys at every level. Compare decoded keys before mapping; escaped spellings cannot bypass duplicate detection. |
| Schema | Reject unknown properties, wrong case/types, missing properties and **all nulls**. Do not coerce scalars, ignore fields or use last-write-wins decoding. |
| Time and output | **5 seconds** for acquisition/validation/ID generation and **1 second total** for output on the supported socket transport below. These are elapsed-time budgets under normal OS scheduling/kernel progress, not a universal hard-real-time process-lifetime guarantee. At most **4,096 bytes per record including newline**, **16 KiB total stdout**; no request override or unbounded draining. |

### Supported subprocess I/O

The deliberately narrow support matrix is:

| Platform / descriptor | Support |
| --- | --- |
| Linux connected `AF_UNIX`, `SOCK_STREAM` sockets (e.g. `socketpair`) | Supported, whether inherited with blocking or nonblocking OFD flags |
| macOS (all descriptors, including sockets) | Rejected: no safe documented per-call send mechanism established |
| Anonymous pipes, named FIFOs (including already nonblocking ones) | Rejected |
| Regular files, TTY/devices, directories, invalid descriptors | Rejected |
| Listening/unconnected sockets, datagram/seqpacket sockets, TCP/other families | Rejected |
| Other platforms | Rejected |

For `run`, stdin and stdout must be supported sockets. Discovery does not inspect/read stdin. Stderr is used only if supported and distinct from stdout. Unsupported input with supported stdout yields a bootstrap `UNSUPPORTED_TRANSPORT` result (exit 1); unsupported stdout permits only the fixed diagnostic on supported stderr. No delivery is promised to unsupported, broken or full endpoints. Ordinary shell pipelines and file redirection are deliberately unsupported; use a socketpair launcher (see [CLI example](../../README.md#experimental-protocol-foundation)). There is **no regular-file best-effort exception**.

The trusted launcher supplies distinct endpoints: Dill is the sole stdin reader and sole stdout/stderr writer, with no concurrent close/replacement/reconfiguration of borrowed descriptors. Peers may write, drain, half-close or close their opposite ends. Send one request then close or `shutdown(SHUT_WR)` the input peer to deliver EOF. Closing only one peer fd while a writable duplicate remains open does not deliver EOF and must not cause acceptance; explicit `SHUT_WR` half-closes the shared socket endpoint, including its duplicates. No ancillary descriptor passing is part of this protocol. These are launcher obligations, not protection against a malicious parent controlling the process.

On Linux every receive/send uses **per-call `MSG_DONTWAIT`**, and every send also uses **`MSG_NOSIGNAL`**. This suppresses SIGPIPE for that call without changing process signal dispositions or shared socket options. No `F_SETFL`, `O_NONBLOCK`, `SO_NOSIGPIPE`, borrowed-fd close, Go poller registration or detached blocking-I/O worker is used. Poll is only a short (at most 10ms) readiness hint between nonblocking attempts; stale readiness cannot turn a recv/send into a wait for data or capacity. Partial sends advance the byte offset; a failed partial record is never followed by a replacement final. One absolute output deadline covers accepted, final and any diagnostic.

`PIPE_BUF` proves atomicity, **not bounded blocking I/O**. In particular XNU pipe readiness can reflect maximum rather than currently allocated capacity; the previous poll + blocking small-write strategy is not supported. The supported socket per-call behavior is specified by [Linux send(2)](https://man7.org/linux/man-pages/man2/send.2.html). **macOS is deliberately unsupported**: a blocking socketpair send with `MSG_DONTWAIT | MSG_NOSIGNAL` blocked under pressure on Darwin 24.5.0, independently reproduced with Go and Python. XNU [sosendcheck](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.121.6/bsd/kern/uipc_socket.c) uses `MSG_DONTWAIT` for the socket-buffer lock wait, but checks `SS_NBIO` or private `MSG_NBIO` for the capacity wait. Public `MSG_DONTWAIT` alone does not establish the required guarantee; private flags, shared `O_NONBLOCK` changes and watchdog-abandoned I/O are not accepted alternatives. Preexisting nonblocking descriptors are not a separate exception in this foundation. Budgets remain practical elapsed-time limits subject to OS scheduling/kernel progress, not hard realtime.

Validate the complete request and EOF before generating an ID or emitting `accepted`. Input read/decode/schema failures produce fixed errors, never quoted tokens, field names, offsets, parser text or input snippets. After syntax/schema validation, validate protocol, operation, then required features in that order. Early size/depth/encoding failures may terminate parsing immediately; no further input drain is required. Encoding, depth, string/array bounds and schema violations use `INVALID_REQUEST`; size overflow uses `REQUEST_TOO_LARGE`. EOF without a complete request is `INVALID_REQUEST`; other input I/O failures use `INTERNAL_ERROR`; failure to reach EOF before the deadline is `DEADLINE_EXCEEDED`.

## Records, results and exits

Each stdout record is one UTF-8 JSON object terminated by LF. No human progress, terminal control sequences or diagnostic text may appear on stdout. Object member order is not semantic; record order and sequence numbers are. Consumers reject unknown properties, invalid types/enums, ID changes, sequence gaps/duplicates, records after a final and multiple finals. A missing/truncated final is an unknown transport outcome to the caller, even if the child exit code appears successful.

### Accepted check

The executor generates `operationId` as `op_` plus 32 lowercase hexadecimal characters from 16 cryptographically random bytes, independent of all input. Generation failure is a preaccept internal failure. IDs correlate one attempt; they are not idempotency keys, credentials, target identities or durable lookup handles. Retrying starts a new attempt.

A successful check emits exactly these two record shapes, using the same generated ID:

```jsonl
{"protocolVersion":"dill.dev/1","recordType":"accepted","operationId":"op_0123456789abcdef0123456789abcdef","operation":"protocol.check","sequence":1}
{"protocolVersion":"dill.dev/1","recordType":"result","operationId":"op_0123456789abcdef0123456789abcdef","operation":"protocol.check","sequence":2,"outcome":"succeeded","effects":"none","cleanup":"notNeeded","changesDetected":false,"errors":[]}
```

There is no production progress event for this immediate check. The NDJSON event codec has the same wire/operation/ID/sequence fields, `recordType:"event"` and `event:"checkProgress"`; it has no payload. It is exercised by synthetic codec fixtures only. A sequence containing such a fixture event uses accepted=1, event=2, result=3; it is not a second production operation or target-progress claim.

Final results carry three independent axes:

| Field | Allowed values | Meaning |
| --- | --- | --- |
| `outcome` | `succeeded`, `rejected`, `failed`, `cancelled`, `unknown` | Completion, not a boolean success flag. |
| `effects` | `none`, `complete`, `partial`, `unknown` | Operation-scoped effects, not application consistency or rollback. |
| `cleanup` | `notNeeded`, `succeeded`, `failed`, `unknown` | Cleanup result independent of the original outcome. |

`changesDetected` is a separate boolean. `errors` is an array of at most 8 `{code,message}` objects, selected only from the fixed table below; no arbitrary details/resource identities are accepted by this codec. The real check always has `effects:none`, `cleanup:notNeeded`, `changesDetected:false`. After acceptance its production outcomes are success, internal failure or cancellation/deadline failure. An accepted request never becomes `rejected` retroactively. Each accepted invocation emits exactly one final result if the process/output transport remains able to report.

### Preaccept failure

Before acceptance, emit one fixed bootstrap result and no accepted/event record. Do not fabricate `operationId`, sequence, wire selection, operation or target identity. Rejections have this exact shape, substituting only another fixed code/message pair:

```json
{"bootstrapVersion":"dill.bootstrap/1","recordType":"result","outcome":"rejected","effects":"none","cleanup":"notNeeded","changesDetected":false,"errors":[{"code":"UNSUPPORTED_PROTOCOL","message":"Unsupported protocol. Use discovery to select a supported version."}]}
```

Preaccept internal failure uses `outcome:failed`; preaccept cancellation/deadline uses `outcome:cancelled`. Their other fields remain unchanged and use the corresponding fixed error. Before acceptance, malformed or unsupported input is never acknowledged as an operation.

### Fixed errors and exit mapping

Messages are exact curated constants. No `err.Error()`, random-input content, caller correlation, environment, Pkl stderr or backend diagnostic may appear in stdout **or stderr**.

| Code | Message | Exit |
| --- | --- | --- |
| `INVALID_ARGUMENTS` | `Invalid protocol command arguments.` | 2 |
| `INVALID_REQUEST` | `Invalid protocol request.` | 2 |
| `REQUEST_TOO_LARGE` | `Protocol request exceeds the size limit.` | 2 |
| `UNSUPPORTED_PROTOCOL` | `Unsupported protocol. Use discovery to select a supported version.` | 2 |
| `UNSUPPORTED_OPERATION` | `Unsupported operation. Only protocol.check is available.` | 2 |
| `UNSUPPORTED_FEATURE` | `A required protocol feature is unavailable.` | 2 |
| `UNSUPPORTED_TRANSPORT` | `Protocol transport is unavailable. Use Unix stream sockets on Linux.` | 1 |
| `INTERNAL_ERROR` | `Protocol check could not complete.` | 1 |
| `CANCELLED` | `Protocol check was cancelled.` | 1 |
| `DEADLINE_EXCEEDED` | `Protocol check exceeded its deadline.` | 1 |
| `OUTPUT_FAILURE` | `Protocol output could not be delivered.` | 1 |

Discovery/check success exits **0**, input/compatibility rejection **2**, internal/cancel/deadline/output failure **1**. This retains the legacy 0/1/2 categories without changing existing commands: legacy plan still exits nonzero on changes; diff still exits zero on drift. A synthetic `changesDetected:true` fixture is not a new production exit-code contract.

Stderr is empty on successful protocol commands and ordinary structured rejection. If stdout delivery fails or cannot finish within the deadline, stop writing it, exit 1 and attempt only the fixed `OUTPUT_FAILURE` code/message line on stderr within the remaining deadline; failure to deliver that line does not authorize blocking. Never append another JSON result after a partially written record. Do not log raw I/O/entropy/parser errors. Output limits are implementation invariants; exceeding them is an internal delivery failure, not permission to truncate JSON and report success.

Handle SIGINT/SIGTERM and the deadline without durable control. Before acceptance, report bootstrap cancellation when possible; afterward, report a single wire final cancellation. Select the final outcome immediately before writing its first byte: a cancellation already observed selects cancellation. Once final writing begins, never rewrite it or append another final; incomplete delivery remains unknown to the caller. A completed success cannot later become cancellation. Forced kill, crash or broken output may prevent a final. There is no cancel-by-ID API, journal, resume, replay, lock or retry guarantee; metadata-only cancellation says nothing about deployment cleanup.

## Secret and artifact boundary

Legacy `config` can emit plaintext env, the current hash includes raw env/other strings, and plan redaction matches selected key names only (`cmd/dill/config.go`, `internal/config/hash.go`, `internal/plan/plan.go`). These paths are **not taint-safe**. Do not wrap them as safe protocol records or certify them via sentinel tests. Do not introduce a plaintext-artifact bypass, hash raw secrets, execute redacted placeholders or strip values silently.

The foundation avoids this conflict by accepting no application payload, echoing no arbitrary input and accessing neither Pkl nor engines/providers. Unknown fields containing secrets must disappear into fixed errors, not logs. This limited boundary does not prove unrestricted Pkl/public strings or workload logs secret-free.

Later #38/#16/#10 work must establish typed references, independent semantic validation, safe producer/disclosure boundaries, opaque rotation/change identities and late target-side resolution before artifact export/apply is offered. Deterministic normalized intent stays separate from mutable target observations/execution history; a rendered artifact is not an approved live-state plan. Pkl-free input must never bypass policy, ownership, target identity or live revalidation. Those capabilities require their own reviewed contracts and tests, not completion of this foundation.

## Acceptance for `protocol-foundation-1`

Gate passage requires implementation review plus recorded passing build/test/vet and contract evidence after the #31/#32 release:

1. Discovery and check match these schemas; strict success emits accepted + one final. Unsupported protocol/operation/required feature and invalid arguments fail closed with fixed errors and exits.
2. Boundary tests cover size/depth/string/feature limits, duplicates including escaped keys, unknown fields, null/type/case errors, UTF-8/surrogates, trailing/multiple/truncated values, EOF/deadline, cancellation, entropy failure, unsupported transports (including blocking pipes/files) and broken/full sockets. Subprocess tests verify budgets with scheduling tolerance, parent access/nonblocking modes unchanged and no detached I/O workers. No input-bearing stderr or legacy setup side effects.
3. Negative sentinel tests place arbitrary strings in keys/values/argv and injected errors; emitted output contains only constants/executor IDs, never those bytes or their hashes. Test spies prove zero config/env-file loading, Pkl resolution/download/evaluation, engine open or provider access on every path.
4. Shared codec fixtures cover accepted/events/final order and **synthetic-only** cancellation, partial effects, changes and unknown outcomes, including failure plus cleanup failure. They must be labelled contract-only, not evidence of deploy/apply/cancel support. Fixed fixture IDs are not runtime ID expectations.
5. The [versioned fixture manifest](../../internal/protocol/testdata/manifest.json) records bootstrap/wire identifiers and expected records/exits for future Brine consumption. Dill owns semantics; no cross-repo implementation/publication is required to pass this foundation. Actual Brine consumer tests remain a full-#19 integration gate.
6. Regression tests preserve existing caps, plan/diff exit behavior and destructive prompts. No deployable IR or target/provider/apply capability is offered; no legacy secret-bearing output is routed through the new namespace.

Until that evidence is reviewed, `protocol-foundation-1` remains **not passed**. Full #19 remains open after foundation passage. Any additional operation, artifact path, target identity/security policy, provider, durable control or changed wire rule requires a separate scope review; none is authorized here.
