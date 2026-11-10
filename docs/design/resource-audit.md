# Legacy resource audit (dill#8)

This describes the current `dill.pkl` Service fields, not the experimental Application/Deployment runtime. `docs/design/examples/` is a Pkl authoring prototype only. Merge is gated on the capability/parity contract in #15; the wire-level tests here are not evidence of cgroup enforcement.

| Field | Normalization and hash | Create / inspect | Plan |
|---|---|---|---|
| `memLimit`, `memReservation` | Decimal quantities converted exactly to bytes, fractional bytes truncated; binary suffixes, including `kb`/`mb`, use powers of 1024. Zero is unset. | Docker HostConfig memory / reservation; Podman OCI memory limit / reservation; both read HostConfig. | Compare requested nonzero bytes; removing a managed limit relies on the configuration hash. |
| `cpus` | Exact decimal converted to nanocpus, floored to a 10,000-nanocpu quantum (100,000µs period); less than one quantum is rejected. | Docker NanoCPUs; Podman quota and period; both read NanoCpus. | Compare normalized nanocpus. |
| `cpuShares` | Nonnegative integer; zero is unset. | Docker CPUShares; Podman OCI shares; both read HostConfig. | Compare requested nonzero shares. |
| `pidsLimit` | Integer >= -1; zero is unset, -1 explicitly requests unlimited. | Docker PidsLimit; Podman OCI pids; both read HostConfig. | Nonzero compared; -1 matches live zero or -1. |
| `cpuset` | String preserved verbatim in hash; no syntax or set canonicalization. | Docker CpusetCpus; Podman OCI CPU cpus; both read HostConfig. | Verbatim comparison, including removal. |

The decimal parser rejects negative, exponential, nonnumeric, overflowing and positive sub-byte values. Legacy `0` CPUs is rejected, unlike omitted CPUs. Equivalent memory/CPU representations share hashes; no migration or new typed runtime fields are introduced. Engine defaults for unset values are intentionally not compared; a lost/missing old hash can therefore hide removal of a limit. Inspect reflects engine-reported configuration, not necessarily effective cgroup values or external changes to them.

## Remaining gaps and sequencing

- The #15 foundation exposes static adapter capability discovery and preflight, not resource or host/cgroup capability checks. Resource support ranges and target-aware pre-mutation rejection remain open, including rootless/cgroup-controller availability, CPU quota minima, shares ranges, cpuset validity/availability, memory minimum and reservation relationships, PID handling and engine-specific unsupported behavior. Today validation does not prove these requests enforceable; engines may reject them after other mutations or behave differently. Existing acceptance of legacy values is not a parity guarantee.
- Podman and Docker inspect representations, unlimited/default values, cgroup v1/v2 enforcement and drift must be verified on real supported versions. Wire fixtures alone do not establish parity. #18 owns the shared conformance fixtures. #29/#32 and #40 own complete pre-mutation validation and target-aware preflight.
- Experimental `DataSize`, bounded CPU/PID values, explicit versioned JSON conversion and typed/legacy equivalence remain future work, dependent on #38 and its prerequisites. Existing design `Resources` uses positive constraints but is not a bounded, serialized runtime contract. Define units, rounding and overflow there before implementation; do not conflate host capacity assertions with enforcement.
- Cpuset equivalence and target CPU availability are not normalized; current raw comparisons can report drift for equivalent spellings. Hash versioning/migration is tracked by #16. Resource removal without a trusted prior hash and effective-state observation need a defined policy.
