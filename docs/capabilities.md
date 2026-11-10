# Current adapter capabilities

`dill capabilities --engine docker` (or `podman`) emits a JSON snapshot of
known **adapter-level** limits without contacting a host. `devices` and
`unpublishedPorts` indicate whether that representation is passed through;
`networkModes` lists Podman explicit modes accepted by preflight. For Docker a
null list means engine-native modes, not a guarantee that a particular mode or
host supports them. This is a small current-schema discovery surface, **not**
the future versioned operation protocol (#19), target preflight (#40), policy,
or a promise that all remaining schema fields work on every host. Unknown
engines fail. Capability checks run for selected services before engine opening,
pulls, orphan removals, networks, volumes, or containers; the Podman adapter
also checks direct `StartService` calls. Validation of an unselected service is
still performed by the existing full-config validator.

| Current schema field | Docker / Podman adapter behavior | Evidence and limits |
| --- | --- | --- |
| image, pullPolicy, containerName | Both use image/name; pull handled by lifecycle | Registry auth and preparation are separate work; engine/host failures remain possible |
| environment, labels, command, user, restart | Both forward values | Engine defaults and inherited image configuration can differ; inspect is not proof of all behavior |
| ports | Both support published mappings | Podman rejects target-only/unpublished ports before mutation; Docker supports them. Host availability is not preflighted |
| volumes | Both map bind, named and tmpfs mounts | Host filesystem/permissions and volume-driver behavior are target-dependent |
| networkMode | Empty uses stack bridge and service alias on both; host and none are passed through | Podman rejects other explicit modes, including container namespace modes; Docker passes modes to engine. Namespace dependencies are deferred to #9 |
| devices | Docker forwards host path, destination and permissions | Podman rejects all device requests before mutation; previous warning-and-ignore behavior is removed. Host device access is not guaranteed on Docker |
| healthcheck | Both forward test, retries and durations | Image health defaults and runtime readiness remain engine-dependent |
| init, capAdd, cpuset | Both forward values | Host privileges and kernel support are not established by discovery |
| memLimit, memReservation, cpus, cpuShares, pidsLimit | Both map normalized resource limits | Quantity precision, host/cgroup behavior and conformance are #8; not certified by this table |
| dependsOn | Dill DAG controls order | Readiness depends on runtime state; no new job or namespace semantics |

`exec`, `top`, and `cp` pin the subprocess to the same endpoint as the SDK.
Docker CLI TLS environment must be consistent with the SDK; Podman CLI must be
installed and able to use the selected URL. Each command inspects the configured
container name, verifies stack/service ownership, and uses the immutable ID;
missing or foreign containers fail without launching the command. Relative
`logs --since` durations are converted to an absolute UTC timestamp before either
API call. `0s` means now and negative durations denote future timestamps;
bare `0` and decimal Unix zero forms mean the Unix epoch. Accepted absolute
inputs use Docker's timestamp parser (including local-time dates interpreted in
the client's timezone), and are converted to UTC for both adapters. Values
outside RFC3339's year range and invalid inputs are rejected before a request.
Status ports are sorted; Podman now uses HTTP 404 rather than error text for
missing-container results. Docker's human status string and Podman's state string
are intentionally not presented as equivalent.

This is a migration change: configurations that relied on Podman silently
ignoring devices, or used unsupported network modes or unpublished ports, now
fail early. Use a verified supported configuration or Docker where appropriate.
A passing capability check is not a host check and does not certify an entire
fixture. Full cross-engine field/no-op/failure conformance remains #18, and live
Podman coverage is needed in CI; no support claim follows from Pkl evaluation.
